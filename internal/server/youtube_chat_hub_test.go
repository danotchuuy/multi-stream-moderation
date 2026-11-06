package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/danotchuuy/multi-stream-moderation/internal/youtubepb"
)

// fakeYouTubeStream stands in for liveChatMessages.streamList. Each
// call to open counts as one YouTube API call; the test pushes
// responses with send and ends the current call with fail.
type fakeYouTubeStream struct {
	mu         sync.Mutex
	opens      int
	pageTokens []string
	onResponse func(*youtubepb.LiveChatMessageListResponse)
	errs       chan error
	opened     chan struct{}
}

func newFakeYouTubeStream() *fakeYouTubeStream {
	return &fakeYouTubeStream{errs: make(chan error), opened: make(chan struct{}, 16)}
}

func (f *fakeYouTubeStream) open(ctx context.Context, accessToken string, pageToken *string, onResponse func(*youtubepb.LiveChatMessageListResponse)) error {
	f.mu.Lock()
	f.opens++
	f.pageTokens = append(f.pageTokens, *pageToken)
	f.onResponse = func(resp *youtubepb.LiveChatMessageListResponse) {
		if next := resp.GetNextPageToken(); next != "" {
			*pageToken = next
		}
		onResponse(resp)
	}
	f.mu.Unlock()
	f.opened <- struct{}{}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-f.errs:
		return err
	}
}

func (f *fakeYouTubeStream) waitOpened(t *testing.T) {
	t.Helper()
	select {
	case <-f.opened:
	case <-time.After(2 * time.Second):
		t.Fatal("stream was never opened")
	}
}

func (f *fakeYouTubeStream) send(nextPageToken string, ids ...string) {
	resp := &youtubepb.LiveChatMessageListResponse{NextPageToken: proto.String(nextPageToken)}
	for _, id := range ids {
		resp.Items = append(resp.Items, &youtubepb.LiveChatMessage{
			Id: proto.String(id),
			Snippet: &youtubepb.LiveChatMessageSnippet{
				Type:              youtubepb.LiveChatMessageSnippet_TypeWrapper_TEXT_MESSAGE_EVENT.Enum(),
				HasDisplayContent: proto.Bool(true),
				DisplayMessage:    proto.String("hi " + id),
			},
		})
	}
	f.mu.Lock()
	onResponse := f.onResponse
	f.mu.Unlock()
	onResponse(resp)
}

func (f *fakeYouTubeStream) openCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens
}

func newTestYouTubeHub(f *fakeYouTubeStream, idle time.Duration) (*youtubeChatHub, chan struct{}) {
	done := make(chan struct{})
	h := newYouTubeChatHub("chat1", f.open,
		func(ctx context.Context, sessionToken string) (string, bool) { return "access-" + sessionToken, true },
		func(*youtubeChatHub) { close(done) },
		idle)
	return h, done
}

func recvEvent(t *testing.T, sub *youtubeChatSubscriber) youtubeChatEvent {
	t.Helper()
	select {
	case ev, ok := <-sub.ch:
		if !ok {
			t.Fatal("subscriber channel closed unexpectedly")
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
	return youtubeChatEvent{}
}

func eventMessageID(t *testing.T, ev youtubeChatEvent) string {
	t.Helper()
	var dto youtubeChatMessageDTO
	if err := json.Unmarshal(ev.Data, &dto); err != nil {
		t.Fatalf("bad event data %s: %v", ev.Data, err)
	}
	return dto.ID
}

func TestYouTubeChatHubSharesOneStreamAcrossTabs(t *testing.T) {
	f := newFakeYouTubeStream()
	h, _ := newTestYouTubeHub(f, time.Minute)
	defer h.cancel()

	a, _, _ := h.subscribe("sessA")
	h.start()
	f.waitOpened(t)
	b, replay, _ := h.subscribe("sessB")
	if len(replay) != 0 {
		t.Fatalf("replay = %d events, want 0 before any messages", len(replay))
	}

	f.send("p1", "m1")
	for _, sub := range []*youtubeChatSubscriber{a, b} {
		if id := eventMessageID(t, recvEvent(t, sub)); id != "m1" {
			t.Errorf("got message %q, want m1", id)
		}
	}

	// A tab that joins later gets the backlog replayed.
	_, replay, _ = h.subscribe("sessC")
	if len(replay) != 1 || eventMessageID(t, replay[0]) != "m1" {
		t.Errorf("late subscriber replay = %v, want [m1]", replay)
	}

	if n := f.openCount(); n != 1 {
		t.Errorf("stream opened %d times for 3 tabs, want 1", n)
	}
}

func TestYouTubeChatHubResumesWithPageTokenAfterTransientError(t *testing.T) {
	f := newFakeYouTubeStream()
	h, _ := newTestYouTubeHub(f, time.Minute)
	defer h.cancel()

	h.subscribe("sessA")
	h.start()
	f.waitOpened(t)
	f.send("p1", "m1")
	f.errs <- status.Error(codes.Unavailable, "blip")

	select {
	case <-f.opened:
	case <-time.After(youtubeStreamReconnectDelay + 2*time.Second):
		t.Fatal("hub did not reconnect after a transient error")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if got := f.pageTokens[len(f.pageTokens)-1]; got != "p1" {
		t.Errorf("reconnect used pageToken %q, want p1", got)
	}
}

func TestYouTubeChatHubFatalErrorEndsAllTabs(t *testing.T) {
	f := newFakeYouTubeStream()
	h, done := newTestYouTubeHub(f, time.Minute)

	a, _, _ := h.subscribe("sessA")
	b, _, _ := h.subscribe("sessB")
	h.start()
	f.waitOpened(t)
	f.errs <- status.Error(codes.FailedPrecondition, "chat ended")

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("hub did not finish on a terminal error")
	}
	for _, sub := range []*youtubeChatSubscriber{a, b} {
		if _, ok := <-sub.ch; ok {
			t.Error("subscriber channel still open after fatal error")
		}
	}
	final := h.finalEvent()
	if final == nil {
		t.Fatal("no final event after fatal error")
	}
	var dto youtubeStreamErrorDTO
	json.Unmarshal(final.Data, &dto)
	if !dto.Fatal || dto.Message != "chat ended" {
		t.Errorf("final event = %+v, want fatal 'chat ended'", dto)
	}
	if _, _, ok := h.subscribe("sessC"); ok {
		t.Error("subscribe succeeded on a finished hub")
	}
	if n := f.openCount(); n != 1 {
		t.Errorf("stream opened %d times, want 1 (no retry after terminal error)", n)
	}
}

func TestYouTubeChatHubStopsAfterIdleTimeout(t *testing.T) {
	f := newFakeYouTubeStream()
	h, done := newTestYouTubeHub(f, 50*time.Millisecond)

	a, _, _ := h.subscribe("sessA")
	h.start()
	f.waitOpened(t)

	// A reconnect within the idle window keeps the hub alive.
	h.unsubscribe(a)
	b, _, ok := h.subscribe("sessA")
	if !ok {
		t.Fatal("resubscribe within idle window failed")
	}
	time.Sleep(100 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("hub stopped despite a connected tab")
	default:
	}

	h.unsubscribe(b)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("hub did not stop after going idle")
	}
	if h.ctx.Err() == nil {
		t.Error("hub's stream context not cancelled after going idle")
	}
	if h.finalEvent() != nil {
		t.Error("idle shutdown should not report a fatal error")
	}
}

func TestYouTubeChatHubBacklogIsCapped(t *testing.T) {
	f := newFakeYouTubeStream()
	h, _ := newTestYouTubeHub(f, time.Minute)
	defer h.cancel()

	h.subscribe("sessA")
	h.start()
	f.waitOpened(t)

	ids := make([]string, youtubeHubBacklogSize+10)
	for i := range ids {
		ids[i] = fmt.Sprintf("m%d", i)
	}
	// The first tab can't hold this many and gets dropped — that's
	// expected; this test is only about what a new tab gets replayed.
	f.send("p1", ids...)

	_, replay, _ := h.subscribe("sessB")
	if len(replay) != youtubeHubBacklogSize {
		t.Fatalf("replay has %d events, want %d", len(replay), youtubeHubBacklogSize)
	}
	if first := eventMessageID(t, replay[0]); first != "m10" {
		t.Errorf("oldest replayed message = %q, want m10", first)
	}
}

func TestYouTubeChatHubDropsSlowTab(t *testing.T) {
	f := newFakeYouTubeStream()
	h, _ := newTestYouTubeHub(f, time.Minute)
	defer h.cancel()

	slow, _, _ := h.subscribe("sessA")
	h.start()
	f.waitOpened(t)

	for i := 0; i <= youtubeHubSubscriberBuffer; i++ {
		f.send("p", fmt.Sprintf("m%d", i))
	}
	n := 0
	for range slow.ch {
		n++
	}
	if n != youtubeHubSubscriberBuffer {
		t.Errorf("slow tab received %d events before being dropped, want %d", n, youtubeHubSubscriberBuffer)
	}
	if h.finalEvent() != nil {
		t.Error("dropping a slow tab should not end the hub")
	}
}
