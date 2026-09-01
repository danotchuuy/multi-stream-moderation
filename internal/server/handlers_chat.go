package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/danotchuuy/multi-stream-moderation/internal/twitch"
)

type chatMessageDTO struct {
	ID          string         `json:"id"`
	Channel     string         `json:"channel"`
	Text        string         `json:"text"`
	Username    string         `json:"username"`
	DisplayName string         `json:"displayName"`
	UserID      string         `json:"userId"`
	Color       string         `json:"color"`
	Mod         bool           `json:"mod"`
	Subscriber  bool           `json:"subscriber"`
	Broadcaster bool           `json:"broadcaster"`
	Badges      []chatBadgeDTO `json:"badges"`
	// FirstMessage reports whether this is the sender's first-ever
	// message in the channel — Twitch only; Kick's chat webhook carries
	// no equivalent signal.
	FirstMessage bool `json:"firstMessage,omitempty"`
	// Reply is the parent message this one is replying to, if any.
	Reply     *chatReplyDTO `json:"reply,omitempty"`
	Timestamp int64         `json:"timestamp"`
}

// chatReplyDTO is the shared "replying to" shape sent to the browser for
// both Twitch (reply-parent-* IRC tags) and Kick (the chat.message.sent
// webhook's replies_to field) messages.
type chatReplyDTO struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName,omitempty"`
	Text        string `json:"text"`
}

// chatBadgeDTO is the shared badge shape sent to the browser for both
// Twitch and Kick messages, so the frontend can use one badge-rendering
// component for both chat panels.
type chatBadgeDTO struct {
	Name string `json:"name"`
	// Version distinguishes tiers within a badge — for Twitch, a
	// subscriber badge's Version is their sub month count; for Kick, a
	// badge's optional count (also usually a subscriber tenure).
	Version string `json:"version,omitempty"`
}

func twitchBadgeDTOs(badges []twitch.Badge) []chatBadgeDTO {
	out := make([]chatBadgeDTO, len(badges))
	for i, b := range badges {
		out[i] = chatBadgeDTO{Name: b.Name, Version: b.Version}
	}
	return out
}

// clearMsgDTO mirrors a Twitch CLEARMSG event: a single chat message was
// deleted.
type clearMsgDTO struct {
	Channel   string `json:"channel"`
	MessageID string `json:"messageId"`
}

// clearChatDTO mirrors a Twitch CLEARCHAT event: a user was banned or
// timed out (UserID set), or the whole chat was cleared (UserID empty).
type clearChatDTO struct {
	Channel         string `json:"channel"`
	UserID          string `json:"userId,omitempty"`
	Username        string `json:"username,omitempty"`
	DurationSeconds int    `json:"durationSeconds,omitempty"`
}

// raidDTO mirrors a Twitch USERNOTICE msg-id=raid event: another
// channel's broadcaster raided this one, bringing their viewers along.
type raidDTO struct {
	Channel     string `json:"channel"`
	DisplayName string `json:"displayName"`
	Login       string `json:"login"`
	ViewerCount int    `json:"viewerCount"`
}

// streamEvent tags what a background IRC callback put on the queue so
// the single SSE-writing goroutine below can marshal and label it
// correctly.
type streamEvent struct {
	kind    string // "message", "clearmsg", "clearchat", or "raid"
	message twitch.Message
	event   twitch.Event
}

// handleChatStream opens a read-only Twitch IRC connection for the
// requested channel and relays chat messages and moderation events
// (message deletion, bans/timeouts) to the browser as server-sent
// events. One IRC connection is created per request; this is fine for a
// locally run, single-viewer tool.
func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	channel, err := normalizeTwitchChannel(r.URL.Query().Get("channel"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	events := make(chan streamEvent, 64)
	enqueue := func(ev streamEvent) {
		select {
		case events <- ev:
		default:
			// Drop the event if the browser can't keep up rather than
			// block the IRC read loop.
		}
	}

	client := twitch.New(twitch.Config{})
	client.OnMessage(func(m twitch.Message) {
		enqueue(streamEvent{kind: "message", message: m})
	})
	client.OnEvent(func(e twitch.Event) {
		switch e.Type {
		case "CLEARMSG":
			enqueue(streamEvent{kind: "clearmsg", event: e})
		case "CLEARCHAT":
			enqueue(streamEvent{kind: "clearchat", event: e})
		case "USERNOTICE":
			if e.Tags["msg-id"] == "raid" {
				enqueue(streamEvent{kind: "raid", event: e})
			}
		}
	})
	if err := client.Join(channel); err != nil {
		http.Error(w, "failed to join channel", http.StatusInternalServerError)
		return
	}

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		client.Run(ctx)
	}()
	defer func() {
		cancel()
		client.Close()
		<-runDone
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			writeStreamEvent(w, ev)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func writeStreamEvent(w http.ResponseWriter, ev streamEvent) {
	switch ev.kind {
	case "message":
		m := ev.message
		dto := chatMessageDTO{
			ID:           m.ID,
			Channel:      m.Channel,
			Text:         m.Text,
			Username:     m.Username,
			DisplayName:  m.DisplayName,
			UserID:       m.UserID,
			Color:        m.Color,
			Mod:          m.Mod,
			Subscriber:   m.Subscriber,
			Broadcaster:  m.Broadcaster,
			Badges:       twitchBadgeDTOs(m.Badges),
			FirstMessage: m.FirstMessage,
			Timestamp:    m.Timestamp.UnixMilli(),
		}
		if m.Reply != nil {
			dto.Reply = &chatReplyDTO{
				Username:    m.Reply.UserLogin,
				DisplayName: m.Reply.DisplayName,
				Text:        m.Reply.Body,
			}
		}
		writeSSE(w, "message", dto)
	case "clearmsg":
		e := ev.event
		writeSSE(w, "clearmsg", clearMsgDTO{
			Channel:   e.Channel,
			MessageID: e.Tags["target-msg-id"],
		})
	case "clearchat":
		e := ev.event
		dto := clearChatDTO{
			Channel: e.Channel,
			UserID:  e.Tags["target-user-id"],
		}
		if len(e.Params) > 1 {
			dto.Username = e.Params[1]
		}
		if d, ok := e.Tags["ban-duration"]; ok {
			if secs, err := strconv.Atoi(d); err == nil {
				dto.DurationSeconds = secs
			}
		}
		writeSSE(w, "clearchat", dto)
	case "raid":
		e := ev.event
		viewerCount, _ := strconv.Atoi(e.Tags["msg-param-viewerCount"])
		login := e.Tags["msg-param-login"]
		displayName := e.Tags["msg-param-displayName"]
		if displayName == "" {
			displayName = login
		}
		writeSSE(w, "raid", raidDTO{
			Channel:     e.Channel,
			DisplayName: displayName,
			Login:       login,
			ViewerCount: viewerCount,
		})
	}
}

func writeSSE(w http.ResponseWriter, event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}
