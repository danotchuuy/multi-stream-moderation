// Package twitch implements a minimal client for Twitch chat over
// EventSub's WebSocket transport. Twitch IRC (the previous transport
// this package used) is deprecated in favor of EventSub; see
// https://dev.twitch.tv/docs/chat/irc-migration/
package twitch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// websocketURL is Twitch's EventSub WebSocket endpoint. A session
// established here is referenced (by its ID) when creating EventSub
// subscriptions via Twitch's Helix API — creating those subscriptions is
// the caller's job (it already owns the access token and client ID),
// not this package's.
const websocketURL = "wss://eventsub.wss.twitch.tv/ws"

// connectTimeout bounds both the dial/handshake and the wait for the
// initial welcome message. Without it, a slow or stalled connection to
// Twitch falls back to gorilla/websocket's 45-second HandshakeTimeout
// default for the dial, and blocks indefinitely waiting for the welcome
// message — either of which would leave a viewer's "connecting..." spinner
// stuck for far longer than a caller should have to wait to find out the
// connection isn't going to succeed.
const connectTimeout = 10 * time.Second

// Notification is one EventSub event delivered over the WebSocket, still
// as raw JSON — the caller decodes Event into whichever struct matches
// Type (ChatMessage for "channel.chat.message", and so on).
type Notification struct {
	// Type is the EventSub subscription type, e.g. "channel.chat.message".
	Type string
	// Event is the notification's "event" payload.
	Event json.RawMessage
}

// Session is a live EventSub WebSocket connection. The zero value is not
// usable; construct with Connect.
type Session struct {
	notifications chan Notification
	closed        chan struct{}
	closeOnce     sync.Once

	mu   sync.Mutex
	conn *websocket.Conn
	id   string
}

// wsEnvelope is the outer shape of every message Twitch sends over the
// EventSub WebSocket, regardless of message_type.
// https://dev.twitch.tv/docs/eventsub/handling-websocket-events/
type wsEnvelope struct {
	Metadata struct {
		MessageType      string `json:"message_type"`
		SubscriptionType string `json:"subscription_type"`
	} `json:"metadata"`
	Payload struct {
		Session *struct {
			ID           string `json:"id"`
			ReconnectURL string `json:"reconnect_url"`
		} `json:"session"`
		Event json.RawMessage `json:"event"`
	} `json:"payload"`
}

// Connect dials Twitch's EventSub WebSocket endpoint and waits for the
// welcome message that every connection starts with. The returned
// Session's ID must be used within 10 seconds (Twitch's limit) to create
// at least one EventSub subscription against it, or Twitch closes the
// connection.
func Connect(ctx context.Context) (*Session, error) {
	return connectTo(ctx, websocketURL)
}

// connectTo is Connect against an arbitrary URL, split out so tests can
// point it at a local fake EventSub server instead of Twitch's own.
func connectTo(ctx context.Context, url string) (*Session, error) {
	dialCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	conn, _, err := websocket.DefaultDialer.DialContext(dialCtx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("twitch: eventsub: dial: %w", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(connectTimeout)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("twitch: eventsub: setting read deadline: %w", err)
	}
	_, data, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("twitch: eventsub: reading welcome message: %w", err)
	}
	var env wsEnvelope
	if err := json.Unmarshal(data, &env); err != nil || env.Metadata.MessageType != "session_welcome" || env.Payload.Session == nil {
		conn.Close()
		return nil, errors.New("twitch: eventsub: expected a welcome message")
	}
	// The deadline above only guards the welcome message; the read loop
	// below should be able to sit idle indefinitely between messages.
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("twitch: eventsub: clearing read deadline: %w", err)
	}

	s := &Session{
		conn:          conn,
		id:            env.Payload.Session.ID,
		notifications: make(chan Notification),
		closed:        make(chan struct{}),
	}
	go s.readLoop()
	return s, nil
}

// ID is the session ID to reference (as transport.session_id) when
// creating EventSub subscriptions that should deliver to this
// connection.
func (s *Session) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// Notifications returns the channel notifications are delivered on. It's
// closed when the session ends, whether from Close, a read error, or a
// reconnect Twitch requested that this session couldn't complete.
func (s *Session) Notifications() <-chan Notification {
	return s.notifications
}

// Close ends the session. A Read loop blocked delivering a notification
// unblocks immediately rather than waiting for a receiver that may never
// come.
func (s *Session) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return nil
	}
	return conn.Close()
}

func (s *Session) readLoop() {
	defer close(s.notifications)

	for {
		s.mu.Lock()
		conn := s.conn
		s.mu.Unlock()

		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var env wsEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}

		switch env.Metadata.MessageType {
		case "session_welcome":
			// Only expected here after a reconnect (the initial welcome
			// is consumed by Connect); a new session ID doesn't disrupt
			// anything since subscriptions already made carry over
			// automatically.
			if env.Payload.Session != nil {
				s.mu.Lock()
				s.id = env.Payload.Session.ID
				s.mu.Unlock()
			}

		case "notification":
			select {
			case s.notifications <- Notification{Type: env.Metadata.SubscriptionType, Event: env.Payload.Event}:
			case <-s.closed:
				return
			}

		case "session_reconnect":
			// Twitch is about to close this connection (e.g. for
			// maintenance) and wants us to move to a new one; existing
			// subscriptions transfer automatically, so nothing else to
			// redo. https://dev.twitch.tv/docs/eventsub/handling-websocket-events/#reconnect-message
			if env.Payload.Session == nil || env.Payload.Session.ReconnectURL == "" {
				return
			}
			newConn, _, err := websocket.DefaultDialer.Dial(env.Payload.Session.ReconnectURL, nil)
			if err != nil {
				return
			}
			old := conn
			s.mu.Lock()
			s.conn = newConn
			s.mu.Unlock()
			old.Close()

		case "session_keepalive":
			// No-op: receiving anything at all already proves the
			// connection is alive.

		case "revocation":
			// A subscription was revoked (the viewer signed out
			// elsewhere, was banned, etc.) — surface it like any other
			// notification so the caller can decide whether to stop.
			select {
			case s.notifications <- Notification{Type: "revocation:" + env.Metadata.SubscriptionType, Event: env.Payload.Event}:
			case <-s.closed:
				return
			}
		}
	}
}
