package server

import (
	"context"
	"crypto/tls"
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
	// youtubeStreamReconnectDelay is the base backoff before re-opening
	// the gRPC stream after a transient error (a network blip, or the
	// server closing the stream normally) — see youtubeNextReconnectDelay
	// for how this grows on repeated failures.
	youtubeStreamReconnectDelay = 3 * time.Second
	// youtubeStreamMaxReconnectDelay caps how far the backoff grows, so a
	// persistently failing stream still retries every couple of minutes
	// rather than backing off forever.
	youtubeStreamMaxReconnectDelay = 2 * time.Minute
	// youtubeStreamHealthyDuration is how long a stream has to stay open
	// before the next failure is treated as a fresh transient blip (reset
	// backoff to the base delay) rather than a continuation of the same
	// problem (keep backing off). Google closing a long-lived stream on
	// its own after a while is normal and shouldn't count against this.
	youtubeStreamHealthyDuration = 30 * time.Second
	// youtubeStreamResourceExhaustedDelay floors the backoff after a
	// RESOURCE_EXHAUSTED (quota) error. These fail instantly — the stream
	// never gets a chance to run long enough to trip
	// youtubeStreamHealthyDuration — so without this floor the reconnect
	// loop would otherwise hammer the base 3-second delay for the rest of
	// the day every time it's called, burning through whatever quota is
	// left with no chance of succeeding.
	youtubeStreamResourceExhaustedDelay = 30 * time.Second
)

// youtubeNextReconnectDelay computes how long to wait before reconnecting
// after a relayYouTubeChatStream attempt that ran for streamAge before
// failing with err. prevDelay is the delay used before that attempt (0
// for the first attempt ever).
func youtubeNextReconnectDelay(prevDelay, streamAge time.Duration, err error) time.Duration {
	if streamAge >= youtubeStreamHealthyDuration {
		prevDelay = 0
	}
	delay := youtubeStreamReconnectDelay
	if prevDelay > 0 {
		if delay = prevDelay * 2; delay > youtubeStreamMaxReconnectDelay {
			delay = youtubeStreamMaxReconnectDelay
		}
	}
	if st, ok := status.FromError(err); ok && st.Code() == codes.ResourceExhausted && delay < youtubeStreamResourceExhaustedDelay {
		delay = youtubeStreamResourceExhaustedDelay
	}
	return delay
}

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
// hands each response to onResponse as it arrives, until the stream
// ends (error, or ctx is cancelled). *pageToken is updated as responses
// arrive so a subsequent call can resume the stream from where this one
// left off, per YouTube's reconnection guidance.
func relayYouTubeChatStream(ctx context.Context, client youtubepb.V3DataLiveChatMessageServiceClient, accessToken, liveChatID string, pageToken *string, onResponse func(*youtubepb.LiveChatMessageListResponse)) error {
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
	for {
		resp, err := stream.Recv()
		if err != nil {
			return err
		}
		if next := resp.GetNextPageToken(); next != "" {
			*pageToken = next
		}
		onResponse(resp)
	}
}

// youtubeGiftSubDTO is YouTube's version of giftSubDTO, carrying the
// event's ID so the browser can de-duplicate it when the hub's backlog
// is replayed after a reconnect.
type youtubeGiftSubDTO struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Count       int    `json:"count"`
}

// youtubeChatEventFromItem converts a chat item into the SSE event to
// relay, or ok=false for item types this app doesn't show.
func youtubeChatEventFromItem(item *youtubepb.LiveChatMessage) (ev youtubeChatEvent, ok bool) {
	snippet := item.GetSnippet()

	switch snippet.GetType() {
	case youtubepb.LiveChatMessageSnippet_TypeWrapper_TEXT_MESSAGE_EVENT:
		if !snippet.GetHasDisplayContent() {
			return ev, false
		}

		ts := time.Now().UnixMilli()
		if parsed, err := time.Parse(time.RFC3339, snippet.GetPublishedAt()); err == nil {
			ts = parsed.UnixMilli()
		}

		author := item.GetAuthorDetails()
		return newYouTubeChatEvent("message", youtubeChatMessageDTO{
			ID:           item.GetId(),
			Text:         snippet.GetDisplayMessage(),
			DisplayName:  author.GetDisplayName(),
			ChannelID:    author.GetChannelId(),
			ProfileImage: author.GetProfileImageUrl(),
			Mod:          author.GetIsChatModerator(),
			Subscriber:   author.GetIsChatSponsor(),
			Broadcaster:  author.GetIsChatOwner(),
			Timestamp:    ts,
		}), true

	case youtubepb.LiveChatMessageSnippet_TypeWrapper_MEMBERSHIP_GIFTING_EVENT:
		// The gifter's own announcement, reporting the whole batch at
		// once. The per-recipient "gift membership received" notices
		// (GIFT_MEMBERSHIP_RECEIVED_EVENT, one per giftee) are
		// deliberately left unhandled — showing each recipient too would
		// just be noise on top of this summary.
		return newYouTubeChatEvent("giftsub", youtubeGiftSubDTO{
			ID:          item.GetId(),
			DisplayName: item.GetAuthorDetails().GetDisplayName(),
			Count:       int(snippet.GetMembershipGiftingDetails().GetGiftMembershipsCount()),
		}), true
	}
	return ev, false
}
