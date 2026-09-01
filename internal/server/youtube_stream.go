package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/danotchuuy/multi-stream-moderation/internal/youtubepb"
)

// youtubeChatGRPCTarget is YouTube's live chat streaming endpoint. See
// https://developers.google.com/youtube/v3/live/streaming-live-chat
const youtubeChatGRPCTarget = "dns:///youtube.googleapis.com:443"

const (
	// youtubeStreamKeepaliveInterval bounds how long the SSE connection
	// to the browser can go silent. liveChatMessages.streamList only
	// pushes a response when there's something to say, so — unlike the
	// old polling loop, which naturally ticked every few seconds — an
	// idle chat could otherwise leave the connection looking dead to a
	// proxy sitting between us and the browser.
	youtubeStreamKeepaliveInterval = 15 * time.Second
	// youtubeStreamReconnectDelay is the backoff before re-opening the
	// gRPC stream after a transient error (e.g. RESOURCE_EXHAUSTED, a
	// network blip, or the server closing the stream normally).
	youtubeStreamReconnectDelay = 3 * time.Second
)

// youtubeChatServiceClient lazily dials the shared gRPC connection used
// for all liveChatMessages.streamList calls. One connection is reused
// across viewers/streams rather than dialing per-request: YouTube's own
// guidance warns that HTTP/2's SETTINGS_MAX_CONCURRENT_STREAMS caps how
// many concurrent streams a single connection can carry, but this
// service only expects a handful of concurrently open moderation dashboards,
// nowhere near that limit.
func (s *Server) youtubeChatServiceClient() (youtubepb.V3DataLiveChatMessageServiceClient, error) {
	s.youtubeChatConnOnce.Do(func() {
		creds := credentials.NewTLS(&tls.Config{})
		s.youtubeChatConn, s.youtubeChatConnErr = grpc.NewClient(youtubeChatGRPCTarget, grpc.WithTransportCredentials(creds))
	})
	if s.youtubeChatConnErr != nil {
		return nil, s.youtubeChatConnErr
	}
	return youtubepb.NewV3DataLiveChatMessageServiceClient(s.youtubeChatConn), nil
}

// youtubeStreamErrTerminal reports whether a streamList error means
// retrying won't help — the caller should surface it to the browser and
// stop, rather than reconnect.
// https://developers.google.com/youtube/v3/live/docs/liveChatMessages/streamList#errors
func youtubeStreamErrTerminal(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		// Includes io.EOF, which is how grpc-go signals the server closed
		// the stream normally — reconnecting (with the last-seen
		// pageToken) is exactly the right response to that.
		return false
	}
	switch st.Code() {
	case codes.PermissionDenied, codes.InvalidArgument, codes.FailedPrecondition, codes.NotFound:
		return true
	default:
		// RESOURCE_EXHAUSTED (rate limited), UNAVAILABLE, and anything
		// else (including a stale access token, which the caller
		// refreshes before reconnecting) are worth retrying.
		return false
	}
}

// relayYouTubeChatStream opens one liveChatMessages.streamList RPC and
// relays text messages to the browser as they arrive, until the stream
// ends (error, or the request context is cancelled). *pageToken is
// updated as responses arrive so a subsequent call can resume the
// stream from where this one left off, per YouTube's reconnection
// guidance.
func (s *Server) relayYouTubeChatStream(ctx context.Context, client youtubepb.V3DataLiveChatMessageServiceClient, accessToken, liveChatID string, pageToken *string, w http.ResponseWriter, flusher http.Flusher) error {
	req := &youtubepb.LiveChatMessageListRequest{
		LiveChatId: proto.String(liveChatID),
		Part:       []string{"snippet", "authorDetails"},
	}
	if *pageToken != "" {
		req.PageToken = proto.String(*pageToken)
	}

	md := metadata.Pairs("authorization", "Bearer "+accessToken)
	stream, err := client.StreamList(metadata.NewOutgoingContext(ctx, md), req)
	if err != nil {
		return err
	}

	type recvResult struct {
		resp *youtubepb.LiveChatMessageListResponse
		err  error
	}
	// Buffered so the background Recv() goroutine can hand off its
	// result and exit even if this function has already returned (e.g.
	// because ctx was cancelled while a Recv() was in flight).
	msgs := make(chan recvResult, 1)
	recv := func() {
		resp, err := stream.Recv()
		msgs <- recvResult{resp, err}
	}
	go recv()

	keepalive := time.NewTicker(youtubeStreamKeepaliveInterval)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case res := <-msgs:
			if res.err != nil {
				return res.err
			}
			if next := res.resp.GetNextPageToken(); next != "" {
				*pageToken = next
			}
			for _, item := range res.resp.GetItems() {
				writeYouTubeChatMessage(w, item)
			}
			flusher.Flush()
			go recv()
		}
	}
}

func writeYouTubeChatMessage(w http.ResponseWriter, item *youtubepb.LiveChatMessage) {
	snippet := item.GetSnippet()

	switch snippet.GetType() {
	case youtubepb.LiveChatMessageSnippet_TypeWrapper_TEXT_MESSAGE_EVENT:
		if !snippet.GetHasDisplayContent() {
			return
		}

		ts := time.Now().UnixMilli()
		if parsed, err := time.Parse(time.RFC3339, snippet.GetPublishedAt()); err == nil {
			ts = parsed.UnixMilli()
		}

		author := item.GetAuthorDetails()
		writeSSE(w, "message", youtubeChatMessageDTO{
			ID:           item.GetId(),
			Text:         snippet.GetDisplayMessage(),
			DisplayName:  author.GetDisplayName(),
			ChannelID:    author.GetChannelId(),
			ProfileImage: author.GetProfileImageUrl(),
			Mod:          author.GetIsChatModerator(),
			Subscriber:   author.GetIsChatSponsor(),
			Broadcaster:  author.GetIsChatOwner(),
			Timestamp:    ts,
		})

	case youtubepb.LiveChatMessageSnippet_TypeWrapper_MEMBERSHIP_GIFTING_EVENT:
		// The gifter's own announcement, reporting the whole batch at
		// once. The per-recipient "gift membership received" notices
		// (GIFT_MEMBERSHIP_RECEIVED_EVENT, one per giftee) are
		// deliberately left unhandled below — showing each recipient
		// too would just be noise on top of this summary.
		writeSSE(w, "giftsub", giftSubDTO{
			DisplayName: item.GetAuthorDetails().GetDisplayName(),
			Count:       int(snippet.GetMembershipGiftingDetails().GetGiftMembershipsCount()),
		})
	}
}
