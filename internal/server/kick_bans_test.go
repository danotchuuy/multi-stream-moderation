package server

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newKickBanTestServer() *Server {
	return &Server{
		kickHub:         newKickHub(),
		kickChatHistory: newChatHistoryCache[int64, kickChatMessageDTO](chatHistoryLimit, func(m kickChatMessageDTO) string { return m.ID }),
		kickBanHistory:  newChatHistoryCache[int64, kickBannedDTO](chatHistoryLimit, func(b kickBannedDTO) string { return b.ID }),
		kickRecentBans:  newKickRecentBans(),
	}
}

func kickWebhookBan(broadcasterID, userID int64, username, moderator, createdAt string, expiresAt *string) kickModerationBannedEvent {
	var ev kickModerationBannedEvent
	ev.Broadcaster.UserID = broadcasterID
	ev.BannedUser.UserID = userID
	ev.BannedUser.Username = username
	ev.Moderator.Username = moderator
	ev.Metadata.Reason = "spam"
	ev.Metadata.CreatedAt = createdAt
	ev.Metadata.ExpiresAt = expiresAt
	return ev
}

func drainKickEvents(sub *kickSubscriber) []kickStreamEvent {
	var out []kickStreamEvent
	for {
		select {
		case ev := <-sub.events:
			out = append(out, ev)
		default:
			return out
		}
	}
}

func TestRecordKickBanMarksHistoryAndNotifiesViewers(t *testing.T) {
	s := newKickBanTestServer()
	s.kickChatHistory.add(1, kickChatMessageDTO{ID: "a", UserID: 42, Username: "spammer"})
	s.kickChatHistory.add(1, kickChatMessageDTO{ID: "b", UserID: 7, Username: "regular"})
	sub, unsub := s.kickHub.subscribe(1)
	defer unsub()

	now := time.Now().UTC()
	expires := now.Add(10 * time.Minute).Format(time.RFC3339)
	s.recordKickBan(1, kickWebhookBan(1, 42, "spammer", "amod", now.Format(time.RFC3339), &expires))

	for _, m := range s.kickChatHistory.recent(1) {
		if want := m.UserID == 42; m.Deleted != want {
			t.Errorf("message %s from user %d: deleted = %v, want %v", m.ID, m.UserID, m.Deleted, want)
		}
	}

	events := drainKickEvents(sub)
	if len(events) != 1 || events[0].kind != "banned" {
		t.Fatalf("viewer got %v, want one banned event", events)
	}
	b := events[0].banned
	if b.Username != "spammer" || b.Moderator != "amod" || b.Reason != "spam" || b.DurationMinutes != 10 {
		t.Errorf("ban notice = %+v, want spammer timed out 10m by amod for spam", b)
	}
	if bans := s.kickBanHistory.recent(1); len(bans) != 1 {
		t.Errorf("ban history has %d entries, want 1", len(bans))
	}
}

func TestRecordKickBanFillsUsernameFromHistory(t *testing.T) {
	s := newKickBanTestServer()
	s.kickChatHistory.add(1, kickChatMessageDTO{ID: "a", UserID: 42, Username: "spammer"})
	s.recordKickBan(1, kickBanFromAction("me", 42, "", "", 0))
	if got := s.kickBanHistory.recent(1)[0].Username; got != "spammer" {
		t.Errorf("username = %q, want spammer (from history)", got)
	}
}

func TestRecordKickBanDeduplicatesAppAndWebhookReports(t *testing.T) {
	s := newKickBanTestServer()
	sub, unsub := s.kickHub.subscribe(1)
	defer unsub()

	// Ban made from this app, then Kick's webhook for the same ban.
	s.recordKickBan(1, kickBanFromAction("me", 42, "spammer", "", 0))
	s.recordKickBan(1, kickWebhookBan(1, 42, "spammer", "me", time.Now().UTC().Format(time.RFC3339), nil))

	if n := len(drainKickEvents(sub)); n != 1 {
		t.Errorf("viewer got %d ban notices for one ban, want 1", n)
	}
}

func TestRecordKickBanKeepsTimeoutThenBan(t *testing.T) {
	s := newKickBanTestServer()
	sub, unsub := s.kickHub.subscribe(1)
	defer unsub()

	s.recordKickBan(1, kickBanFromAction("me", 42, "spammer", "", 5))
	s.recordKickBan(1, kickBanFromAction("me", 42, "spammer", "", 0))

	if n := len(drainKickEvents(sub)); n != 2 {
		t.Errorf("viewer got %d notices for a timeout then a ban, want 2", n)
	}
}

func TestMarkIfSentBeforeBanCatchesLateMessages(t *testing.T) {
	s := newKickBanTestServer()
	banAt := time.Now().UTC().Truncate(time.Second)
	s.recordKickBan(1, kickWebhookBan(1, 42, "spammer", "amod", banAt.Format(time.RFC3339), nil))

	late := kickChatMessageDTO{UserID: 42, Timestamp: banAt.Add(-time.Second).UnixMilli()}
	s.markIfSentBeforeBan(1, &late)
	if !late.Deleted {
		t.Error("message sent before the ban but delivered after it was not marked deleted")
	}

	after := kickChatMessageDTO{UserID: 42, Timestamp: banAt.Add(time.Minute).UnixMilli()}
	s.markIfSentBeforeBan(1, &after)
	if after.Deleted {
		t.Error("message sent after the ban (e.g. once a timeout expired) was marked deleted")
	}

	other := kickChatMessageDTO{UserID: 7, Timestamp: banAt.Add(-time.Second).UnixMilli()}
	s.markIfSentBeforeBan(1, &other)
	if other.Deleted {
		t.Error("another user's message was marked deleted")
	}
}

func TestWriteKickHistoryInterleavesBans(t *testing.T) {
	messages := []kickChatMessageDTO{
		{ID: "m1", Timestamp: 100},
		{ID: "m2", Timestamp: 300},
	}
	bans := []kickBannedDTO{
		{ID: "old", Timestamp: 50},
		{ID: "b1", Timestamp: 200},
		{ID: "b2", Timestamp: 400},
	}
	rec := httptest.NewRecorder()
	writeKickHistory(rec, "chan", messages, bans)

	body := rec.Body.String()
	var order []string
	for _, id := range []string{"old", "m1", "b1", "m2", "b2"} {
		if i := strings.Index(body, `"id":"`+id+`"`); i >= 0 {
			order = append(order, id)
		}
	}
	if got := strings.Join(order, ","); got != "m1,b1,m2,b2" {
		t.Errorf("replayed %s, want m1,b1,m2,b2 (ban older than all messages skipped)", got)
	}
	positions := []int{
		strings.Index(body, `"id":"m1"`),
		strings.Index(body, `"id":"b1"`),
		strings.Index(body, `"id":"m2"`),
		strings.Index(body, `"id":"b2"`),
	}
	for i := 1; i < len(positions); i++ {
		if positions[i] < positions[i-1] {
			t.Fatalf("history out of time order: %s", body)
		}
	}
}

func TestKickHubSignalsLaggedViewerInsteadOfSilentlyDropping(t *testing.T) {
	h := newKickHub()
	sub, unsub := h.subscribe(1)
	defer unsub()

	for i := 0; i <= cap(sub.events); i++ {
		h.publish(1, kickStreamEvent{kind: "message"})
	}
	select {
	case <-sub.lagged:
	default:
		t.Fatal("viewer that overflowed its buffer was not signalled as lagged")
	}
}
