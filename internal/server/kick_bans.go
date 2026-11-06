package server

import (
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"
)

const (
	// kickBanDedupeWindow is how long after a ban an identical one (same
	// user, same ban-vs-timeout) is treated as the same ban reported
	// twice — once by this app when it made the ban, and again by Kick's
	// moderation.banned webhook for it.
	kickBanDedupeWindow = 30 * time.Second
	// kickBanLateMessageWindow is how long a ban is remembered for
	// catching messages delivered after it. Kick sends each webhook
	// independently, so a banned user's last message can arrive after
	// the ban itself; without this it would show as a live message.
	kickBanLateMessageWindow = 2 * time.Minute
)

type kickBanKey struct {
	broadcasterID int64
	userID        int64
	permanent     bool
}

// kickRecentBans remembers bans from the last few minutes, for
// de-duplication and for catching late-delivered messages (see the
// constants above).
type kickRecentBans struct {
	mu   sync.Mutex
	bans map[kickBanKey]time.Time
}

func newKickRecentBans() *kickRecentBans {
	return &kickRecentBans{bans: make(map[kickBanKey]time.Time)}
}

// record notes a ban made at `at`, reporting false if it's a duplicate
// of one already recorded.
func (r *kickRecentBans) record(key kickBanKey, at time.Time) (isNew bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for k, t := range r.bans {
		if now.Sub(t) > kickBanLateMessageWindow {
			delete(r.bans, k)
		}
	}
	if prev, ok := r.bans[key]; ok && at.Sub(prev).Abs() < kickBanDedupeWindow {
		return false
	}
	r.bans[key] = at
	return true
}

// bannedAt returns when userID was most recently banned or timed out
// in broadcasterID's channel, if within kickBanLateMessageWindow.
func (r *kickRecentBans) bannedAt(broadcasterID, userID int64) (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var latest time.Time
	for _, permanent := range []bool{false, true} {
		if t, ok := r.bans[kickBanKey{broadcasterID, userID, permanent}]; ok && t.After(latest) {
			latest = t
		}
	}
	if latest.IsZero() || time.Since(latest) > kickBanLateMessageWindow {
		return time.Time{}, false
	}
	return latest, true
}

// recordKickBan is the one place a Kick ban or timeout is handled,
// whether it came from Kick's moderation.banned webhook (a ban made
// anywhere, including kick.com) or from this app's own ban/timeout
// endpoints (so it still shows up even if the webhook subscription is
// missing or slow). It marks the user's buffered messages deleted,
// keeps the notice for viewers who connect later, and relays it to
// every viewer now connected.
func (s *Server) recordKickBan(broadcasterID int64, ev kickModerationBannedEvent) {
	at := time.Now()
	if t, err := time.Parse(time.RFC3339, ev.Metadata.CreatedAt); err == nil {
		at = t
	}

	durationMinutes := 0
	permanent := true
	if ev.Metadata.ExpiresAt != nil && *ev.Metadata.ExpiresAt != "" {
		permanent = false
		if expires, err := time.Parse(time.RFC3339, *ev.Metadata.ExpiresAt); err == nil {
			durationMinutes = max(1, int(math.Round(expires.Sub(at).Minutes())))
		} else {
			durationMinutes = 1
		}
	}

	userID := ev.BannedUser.UserID
	if !s.kickRecentBans.record(kickBanKey{broadcasterID, userID, permanent}, at) {
		return
	}

	username := ev.BannedUser.Username
	s.kickChatHistory.update(broadcasterID, func(m *kickChatMessageDTO) {
		if m.UserID == userID {
			m.Deleted = true
			if username == "" {
				username = m.Username
			}
		}
	})

	dto := kickBannedDTO{
		ID:              fmt.Sprintf("ban-%d-%d", userID, at.UnixMilli()),
		UserID:          userID,
		Username:        username,
		Moderator:       ev.Moderator.Username,
		Reason:          ev.Metadata.Reason,
		DurationMinutes: durationMinutes,
		Timestamp:       at.UnixMilli(),
	}
	s.kickBanHistory.add(broadcasterID, dto)
	s.kickHub.publish(broadcasterID, kickStreamEvent{kind: "banned", banned: dto})
}

// markIfSentBeforeBan flags a message deleted if its sender was banned
// at or after the time it was sent — i.e. it was delivered late, after
// the ban that should have removed it.
func (s *Server) markIfSentBeforeBan(broadcasterID int64, dto *kickChatMessageDTO) {
	if bannedAt, ok := s.kickRecentBans.bannedAt(broadcasterID, dto.UserID); ok && dto.Timestamp <= bannedAt.UnixMilli() {
		dto.Deleted = true
	}
}

// kickBanFromAction builds the event recordKickBan expects for a ban or
// timeout (durationMinutes > 0) this app just made itself.
func kickBanFromAction(moderator string, userID int64, username, reason string, durationMinutes int) kickModerationBannedEvent {
	now := time.Now()
	var ev kickModerationBannedEvent
	ev.Moderator.Username = moderator
	ev.BannedUser.UserID = userID
	ev.BannedUser.Username = username
	ev.Metadata.Reason = reason
	ev.Metadata.CreatedAt = now.Format(time.RFC3339)
	if durationMinutes > 0 {
		expires := now.Add(time.Duration(durationMinutes) * time.Minute).Format(time.RFC3339)
		ev.Metadata.ExpiresAt = &expires
	}
	return ev
}

// writeKickHistory replays buffered messages and ban notices to a newly
// connected viewer, interleaved in time order. Ban notices older than
// the oldest buffered message are skipped: there's nothing on screen
// they'd explain.
func writeKickHistory(w http.ResponseWriter, channel string, messages []kickChatMessageDTO, bans []kickBannedDTO) {
	if len(messages) == 0 {
		return
	}
	oldest := messages[0].Timestamp
	i, j := 0, 0
	for i < len(messages) || j < len(bans) {
		if j < len(bans) && bans[j].Timestamp < oldest {
			j++
			continue
		}
		if j >= len(bans) || (i < len(messages) && messages[i].Timestamp <= bans[j].Timestamp) {
			m := messages[i]
			m.Channel = channel
			writeSSE(w, "message", m)
			i++
		} else {
			b := bans[j]
			b.Channel = channel
			writeSSE(w, "banned", b)
			j++
		}
	}
}
