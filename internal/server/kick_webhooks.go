package server

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
)

// kickEventMessageSent / kickEventModerationBanned are the two webhook
// event types this app subscribes to.
// https://docs.kick.com/events/event-types
const (
	kickEventMessageSent    = "chat.message.sent"
	kickEventModerationBans = "moderation.banned"
	kickEventVersion        = 1
)

// --- Signature verification ---------------------------------------------

// kickPublicKeyCache lazily fetches and caches Kick's webhook-signing
// public key, refetching once if a verification fails in case the key
// was rotated.
type kickPublicKeyCache struct {
	mu  sync.Mutex
	key *rsa.PublicKey
}

func (c *kickPublicKeyCache) get(ctx context.Context, forceRefresh bool) (*rsa.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.key != nil && !forceRefresh {
		return c.key, nil
	}

	var out struct {
		Data struct {
			PublicKey string `json:"public_key"`
		} `json:"data"`
	}
	if err := kickAPIRequest(ctx, http.MethodGet, kickAPIBase+"/public-key", "", nil, &out); err != nil {
		return nil, fmt.Errorf("fetching kick public key: %w", err)
	}

	block, _ := pem.Decode([]byte(out.Data.PublicKey))
	if block == nil {
		return nil, errors.New("kick public key is not PEM-encoded")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("kick public key is not an RSA key")
	}

	c.key = key
	return c.key, nil
}

// verify checks a webhook delivery's signature against Kick's public
// key, per https://docs.kick.com/events/webhook-security: the signed
// message is "{messageID}.{timestamp}.{rawBody}", RSA-PKCS1v15/SHA-256.
func (c *kickPublicKeyCache) verify(ctx context.Context, messageID, timestamp string, body, signatureB64 []byte) error {
	sig := make([]byte, base64.StdEncoding.DecodedLen(len(signatureB64)))
	n, err := base64.StdEncoding.Decode(sig, signatureB64)
	if err != nil {
		return fmt.Errorf("decoding signature: %w", err)
	}
	sig = sig[:n]

	signed := []byte(messageID + "." + timestamp + "." + string(body))
	hashed := sha256.Sum256(signed)

	key, err := c.get(ctx, false)
	if err != nil {
		return err
	}
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, hashed[:], sig); err != nil {
		// The key may have rotated; refetch once and retry before
		// giving up.
		key, ferr := c.get(ctx, true)
		if ferr != nil {
			return err
		}
		return rsa.VerifyPKCS1v15(key, crypto.SHA256, hashed[:], sig)
	}
	return nil
}

// --- Event subscriptions --------------------------------------------------

type kickEventSubscription struct {
	ID    string `json:"id"`
	Event string `json:"event"`
}

// ensureKickEventSubscription makes sure broadcasterUserID has an active
// webhook subscription for eventName, creating one if not. Duplicate
// subscriptions would make Kick deliver every event twice, so this
// checks first rather than always posting.
func ensureKickEventSubscription(ctx context.Context, accessToken string, broadcasterUserID int64, eventName string) error {
	var existing struct {
		Data []kickEventSubscription `json:"data"`
	}
	listURL := kickAPIBase + "/events/subscriptions?broadcaster_user_id=" + strconv.FormatInt(broadcasterUserID, 10)
	if err := kickAPIRequest(ctx, http.MethodGet, listURL, accessToken, nil, &existing); err != nil {
		return err
	}
	for _, sub := range existing.Data {
		if sub.Event == eventName {
			return nil
		}
	}

	body, err := json.Marshal(struct {
		Events []struct {
			Name    string `json:"name"`
			Version int    `json:"version"`
		} `json:"events"`
		Method string `json:"method"`
	}{
		Events: []struct {
			Name    string `json:"name"`
			Version int    `json:"version"`
		}{{Name: eventName, Version: kickEventVersion}},
		Method: "webhook",
	})
	if err != nil {
		return err
	}
	return kickAPIRequest(ctx, http.MethodPost, kickAPIBase+"/events/subscriptions", accessToken, body, nil)
}

// --- Fan-out ---------------------------------------------------------------

// kickBadge and kickIdentity describe a chatter's badges (moderator,
// broadcaster, subscriber, ...), used to decide which moderation
// controls are safe to show for a message — mirrors how the Twitch
// integration reads Mod/Broadcaster off IRC tags.
type kickBadge struct {
	Type string `json:"type"`
}

type kickIdentity struct {
	UsernameColor string      `json:"username_color"`
	Badges        []kickBadge `json:"badges"`
}

func (id *kickIdentity) hasBadge(badgeType string) bool {
	if id == nil {
		return false
	}
	for _, b := range id.Badges {
		if b.Type == badgeType {
			return true
		}
	}
	return false
}

// kickChatMessageEvent is the chat.message.sent webhook payload.
// https://docs.kick.com/events/event-types
type kickChatMessageEvent struct {
	MessageID   string `json:"message_id"`
	Broadcaster struct {
		UserID      int64  `json:"user_id"`
		ChannelSlug string `json:"channel_slug"`
	} `json:"broadcaster"`
	Sender struct {
		UserID   int64         `json:"user_id"`
		Username string        `json:"username"`
		Identity *kickIdentity `json:"identity"`
	} `json:"sender"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

// kickModerationBannedEvent is the moderation.banned webhook payload.
type kickModerationBannedEvent struct {
	Broadcaster struct {
		UserID int64 `json:"user_id"`
	} `json:"broadcaster"`
	BannedUser struct {
		UserID   int64  `json:"user_id"`
		Username string `json:"username"`
	} `json:"banned_user"`
	Metadata struct {
		Reason    string  `json:"reason"`
		ExpiresAt *string `json:"expires_at"`
	} `json:"metadata"`
}

// kickStreamEvent tags which of the two payloads a fan-out subscriber
// received, mirroring streamEvent in handlers_chat.go.
type kickStreamEvent struct {
	kind    string // "message" or "banned"
	message kickChatMessageEvent
	banned  kickModerationBannedEvent
}

// kickHub fans webhook deliveries for a broadcaster out to every browser
// tab currently streaming that channel's chat (there's exactly one
// webhook delivery per event — Kick doesn't send one per subscriber — so
// this is what turns it into a per-viewer SSE stream).
type kickHub struct {
	mu          sync.Mutex
	subscribers map[int64]map[chan kickStreamEvent]struct{}
}

func newKickHub() *kickHub {
	return &kickHub{subscribers: make(map[int64]map[chan kickStreamEvent]struct{})}
}

func (h *kickHub) subscribe(broadcasterUserID int64) (ch chan kickStreamEvent, unsubscribe func()) {
	ch = make(chan kickStreamEvent, 64)

	h.mu.Lock()
	if h.subscribers[broadcasterUserID] == nil {
		h.subscribers[broadcasterUserID] = make(map[chan kickStreamEvent]struct{})
	}
	h.subscribers[broadcasterUserID][ch] = struct{}{}
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		delete(h.subscribers[broadcasterUserID], ch)
		if len(h.subscribers[broadcasterUserID]) == 0 {
			delete(h.subscribers, broadcasterUserID)
		}
		h.mu.Unlock()
		close(ch)
	}
}

func (h *kickHub) publish(broadcasterUserID int64, ev kickStreamEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subscribers[broadcasterUserID] {
		select {
		case ch <- ev:
		default:
			// Drop the event if that particular viewer's stream can't
			// keep up, rather than blocking delivery to everyone else.
		}
	}
}

// --- Webhook receiver --------------------------------------------------

// handleKickWebhook receives event deliveries from Kick's servers (not
// the browser — there's no session cookie here, no CORS applies). It
// verifies the RSA signature, then fans the payload out to any
// connected chat-stream viewers for that broadcaster.
func (s *Server) handleKickWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "could not read body", http.StatusBadRequest)
		return
	}

	messageID := r.Header.Get("Kick-Event-Message-Id")
	timestamp := r.Header.Get("Kick-Event-Message-Timestamp")
	eventType := r.Header.Get("Kick-Event-Type")
	signature := r.Header.Get("Kick-Event-Signature")
	if messageID == "" || timestamp == "" || signature == "" {
		http.Error(w, "missing signature headers", http.StatusBadRequest)
		return
	}

	if err := s.kickPublicKey.verify(r.Context(), messageID, timestamp, body, []byte(signature)); err != nil {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	switch eventType {
	case kickEventMessageSent:
		var msg kickChatMessageEvent
		if err := json.Unmarshal(body, &msg); err == nil {
			s.kickHub.publish(msg.Broadcaster.UserID, kickStreamEvent{kind: "message", message: msg})
		}
	case kickEventModerationBans:
		var banned kickModerationBannedEvent
		if err := json.Unmarshal(body, &banned); err == nil {
			s.kickHub.publish(banned.Broadcaster.UserID, kickStreamEvent{kind: "banned", banned: banned})
		}
	}

	w.WriteHeader(http.StatusOK)
}
