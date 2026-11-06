package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/danotchuuy/multi-stream-moderation/internal/youtubepb"
)

const (
	// youtubeHubBacklogSize is how many recent chat events a hub keeps to
	// replay to a newly connected tab, so it doesn't start blank (and so
	// a tab whose EventSource reconnected catches up on what it missed).
	youtubeHubBacklogSize = 200
	// youtubeHubIdleTimeout is how long a hub keeps its YouTube stream
	// open after its last tab disconnects. Long enough to ride out a page
	// reload or a deploy's reconnect without paying to re-open the stream
	// (and losing its pageToken/backoff state), short enough not to keep
	// streaming a chat nobody is watching.
	youtubeHubIdleTimeout = 30 * time.Second
	// youtubeHubSubscriberBuffer bounds how far a single tab can fall
	// behind. A tab that fills it is disconnected rather than allowed to
	// stall the hub; its EventSource reconnects and catches up from the
	// backlog.
	youtubeHubSubscriberBuffer = 256
)

// youtubeChatEvent is one pre-encoded SSE event, so a hub serializes
// each chat message once no matter how many tabs it's relayed to.
type youtubeChatEvent struct {
	Name string
	Data []byte
}

func newYouTubeChatEvent(name string, payload any) youtubeChatEvent {
	data, err := json.Marshal(payload)
	if err != nil {
		data = []byte("{}")
	}
	return youtubeChatEvent{Name: name, Data: data}
}

func (ev youtubeChatEvent) writeTo(w io.Writer) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Name, ev.Data)
}

// youtubeChatSubscriber is one connected tab. ch is closed when the hub
// stops relaying to it — because the hub ended (see youtubeChatHub.final
// for why), or because the tab fell too far behind.
type youtubeChatSubscriber struct {
	ch           chan youtubeChatEvent
	sessionToken string
}

// youtubeStreamFunc opens one liveChatMessages.streamList call and
// blocks until it ends, handing each response to onResponse and keeping
// *pageToken up to date. relayYouTubeChatStream in production; a fake in
// tests.
type youtubeStreamFunc func(ctx context.Context, accessToken string, pageToken *string, onResponse func(*youtubepb.LiveChatMessageListResponse)) error

// youtubeChatHub owns the single YouTube stream for one live chat and
// fans it out to every tab watching that chat. Without it, every tab
// (and every EventSource reconnect) opened its own streamList call,
// multiplying API quota use, and the reconnect backoff and pageToken
// reset each time a tab reconnected.
//
// The stream is read with whichever connected viewer's session is
// still valid, so every tab sees the same messages regardless of whose
// token fetched them.
type youtubeChatHub struct {
	liveChatID  string
	open        youtubeStreamFunc
	accessToken func(ctx context.Context, sessionToken string) (string, bool)
	onDone      func(*youtubeChatHub)
	idleTimeout time.Duration

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	subs    map[*youtubeChatSubscriber]struct{}
	backlog []youtubeChatEvent
	// status is the current non-fatal streamError (e.g. quota
	// exhausted), replayed to newly connected tabs until the stream
	// recovers.
	status *youtubeChatEvent
	// final is the fatal streamError the hub ended with, if any. Tabs
	// read it after their channel closes rather than having it sent down
	// the channel, so it can't be lost to a full buffer.
	final *youtubeChatEvent
	// lastSessionToken lets the hub keep reconnecting during the idle
	// window, when no tab is connected to borrow a session from.
	lastSessionToken string
	idleTimer        *time.Timer
	done             bool
}

func newYouTubeChatHub(liveChatID string, open youtubeStreamFunc, accessToken func(context.Context, string) (string, bool), onDone func(*youtubeChatHub), idleTimeout time.Duration) *youtubeChatHub {
	ctx, cancel := context.WithCancel(context.Background())
	return &youtubeChatHub{
		liveChatID:  liveChatID,
		open:        open,
		accessToken: accessToken,
		onDone:      onDone,
		idleTimeout: idleTimeout,
		ctx:         ctx,
		cancel:      cancel,
		subs:        make(map[*youtubeChatSubscriber]struct{}),
	}
}

func (h *youtubeChatHub) start() {
	go h.run()
}

// subscribe registers a tab and returns the events to replay to it
// first. Taking the snapshot and registering under the same lock means
// the tab can't miss or double-receive an event published in between.
// ok is false if the hub has already ended; the caller should start a
// new one.
func (h *youtubeChatHub) subscribe(sessionToken string) (sub *youtubeChatSubscriber, replay []youtubeChatEvent, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.done {
		return nil, nil, false
	}
	sub = &youtubeChatSubscriber{
		ch:           make(chan youtubeChatEvent, youtubeHubSubscriberBuffer),
		sessionToken: sessionToken,
	}
	h.subs[sub] = struct{}{}
	h.lastSessionToken = sessionToken
	if h.idleTimer != nil {
		h.idleTimer.Stop()
		h.idleTimer = nil
	}

	replay = append(replay, h.backlog...)
	if h.status != nil {
		replay = append(replay, *h.status)
	}
	return sub, replay, true
}

func (h *youtubeChatHub) unsubscribe(sub *youtubeChatSubscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeLocked(sub)
}

// finalEvent returns the fatal error the hub ended with, if any.
func (h *youtubeChatHub) finalEvent() *youtubeChatEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.final
}

func (h *youtubeChatHub) removeLocked(sub *youtubeChatSubscriber) {
	if _, ok := h.subs[sub]; !ok {
		return
	}
	delete(h.subs, sub)
	close(sub.ch)
	if len(h.subs) == 0 && !h.done {
		if h.idleTimer != nil {
			h.idleTimer.Stop()
		}
		h.idleTimer = time.AfterFunc(h.idleTimeout, h.stopIfIdle)
	}
}

func (h *youtubeChatHub) stopIfIdle() {
	h.mu.Lock()
	if h.done || len(h.subs) > 0 {
		h.mu.Unlock()
		return
	}
	h.done = true
	h.mu.Unlock()

	h.cancel()
	h.onDone(h)
}

// finish ends the hub with a fatal error, delivered to every connected
// tab (see final).
func (h *youtubeChatHub) finish(fatal youtubeChatEvent) {
	h.mu.Lock()
	if h.done {
		h.mu.Unlock()
		return
	}
	h.done = true
	h.final = &fatal
	if h.idleTimer != nil {
		h.idleTimer.Stop()
		h.idleTimer = nil
	}
	for sub := range h.subs {
		delete(h.subs, sub)
		close(sub.ch)
	}
	h.mu.Unlock()

	h.cancel()
	h.onDone(h)
}

// publishLocked sends ev to every tab, disconnecting any that can't
// keep up.
func (h *youtubeChatHub) publishLocked(ev youtubeChatEvent) {
	for sub := range h.subs {
		select {
		case sub.ch <- ev:
		default:
			h.removeLocked(sub)
		}
	}
}

func (h *youtubeChatHub) handleResponse(resp *youtubepb.LiveChatMessageListResponse) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Any response at all means the stream is healthy again.
	h.status = nil
	for _, item := range resp.GetItems() {
		ev, ok := youtubeChatEventFromItem(item)
		if !ok {
			continue
		}
		h.backlog = append(h.backlog, ev)
		if over := len(h.backlog) - youtubeHubBacklogSize; over > 0 {
			h.backlog = append(h.backlog[:0:0], h.backlog[over:]...)
		}
		h.publishLocked(ev)
	}
}

func (h *youtubeChatHub) setStatus(ev youtubeChatEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status = &ev
	h.publishLocked(ev)
}

// pickAccessToken returns a usable access token from any connected
// tab's session, falling back to the most recent one seen.
func (h *youtubeChatHub) pickAccessToken() (string, bool) {
	h.mu.Lock()
	candidates := make([]string, 0, len(h.subs)+1)
	seen := make(map[string]bool)
	for sub := range h.subs {
		if !seen[sub.sessionToken] {
			seen[sub.sessionToken] = true
			candidates = append(candidates, sub.sessionToken)
		}
	}
	if h.lastSessionToken != "" && !seen[h.lastSessionToken] {
		candidates = append(candidates, h.lastSessionToken)
	}
	h.mu.Unlock()

	for _, token := range candidates {
		if at, ok := h.accessToken(h.ctx, token); ok {
			return at, true
		}
	}
	return "", false
}

func youtubeFatalEvent(message string) youtubeChatEvent {
	return newYouTubeChatEvent("streamError", youtubeStreamErrorDTO{Message: message, Fatal: true})
}

// run keeps the YouTube stream open, reconnecting with backoff, until
// the chat ends, no viewer has a usable session, or the hub goes idle.
func (h *youtubeChatHub) run() {
	var pageToken string
	var reconnectDelay time.Duration

	for {
		accessToken, ok := h.pickAccessToken()
		if !ok {
			h.finish(youtubeFatalEvent("YouTube sign-in expired — sign in again"))
			return
		}

		streamStart := time.Now()
		err := h.open(h.ctx, accessToken, &pageToken, h.handleResponse)
		if h.ctx.Err() != nil {
			return
		}
		if youtubeStreamErrTerminal(err) {
			// Chat ended, access revoked, or the ID is no longer valid.
			st, _ := status.FromError(err)
			h.finish(youtubeFatalEvent(st.Message()))
			return
		}

		reconnectDelay = youtubeNextReconnectDelay(reconnectDelay, time.Since(streamStart), err)
		if st, ok := status.FromError(err); ok && st.Code() == codes.ResourceExhausted {
			// Surface this rather than leaving tabs silently stuck on
			// "connecting..." — reconnecting keeps happening here
			// (quota resets daily), this is just visibility.
			h.setStatus(newYouTubeChatEvent("streamError", youtubeStreamErrorDTO{Message: "YouTube API quota exceeded — retrying automatically"}))
		}

		select {
		case <-h.ctx.Done():
			return
		case <-time.After(reconnectDelay):
		}
	}
}

// subscribeYouTubeChat attaches a tab to the hub for liveChatID,
// starting one if none is running.
func (s *Server) subscribeYouTubeChat(liveChatID, sessionToken string) (*youtubeChatHub, *youtubeChatSubscriber, []youtubeChatEvent, error) {
	client, err := s.youtubeChatServiceClient()
	if err != nil {
		return nil, nil, nil, err
	}

	s.youtubeHubsMu.Lock()
	defer s.youtubeHubsMu.Unlock()
	if s.youtubeHubs == nil {
		s.youtubeHubs = make(map[string]*youtubeChatHub)
	}
	if h := s.youtubeHubs[liveChatID]; h != nil {
		if sub, replay, ok := h.subscribe(sessionToken); ok {
			return h, sub, replay, nil
		}
	}

	open := func(ctx context.Context, accessToken string, pageToken *string, onResponse func(*youtubepb.LiveChatMessageListResponse)) error {
		return relayYouTubeChatStream(ctx, client, accessToken, liveChatID, pageToken, onResponse)
	}
	accessToken := func(ctx context.Context, sessionToken string) (string, bool) {
		sess := s.youtubeSessionByToken(ctx, sessionToken)
		if sess == nil {
			return "", false
		}
		return sess.AccessToken, true
	}
	h := newYouTubeChatHub(liveChatID, open, accessToken, s.removeYouTubeChatHub, youtubeHubIdleTimeout)
	s.youtubeHubs[liveChatID] = h
	// Subscribe before starting, so the hub's first token lookup already
	// sees this tab's session.
	sub, replay, _ := h.subscribe(sessionToken)
	h.start()
	return h, sub, replay, nil
}

func (s *Server) removeYouTubeChatHub(h *youtubeChatHub) {
	s.youtubeHubsMu.Lock()
	defer s.youtubeHubsMu.Unlock()
	if s.youtubeHubs[h.liveChatID] == h {
		delete(s.youtubeHubs, h.liveChatID)
	}
}
