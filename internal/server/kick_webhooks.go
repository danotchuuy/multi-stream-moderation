package server

import (
	"bytes"
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
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// kickEventMessageSent / kickEventModerationBans / kickEventSubscriptionGifts
// / kickEventKicksGifted are the webhook event types this app subscribes
// to.
// https://docs.kick.com/events/event-types
const (
	kickEventMessageSent       = "chat.message.sent"
	kickEventModerationBans    = "moderation.banned"
	kickEventSubscriptionGifts = "channel.subscription.gifts"
	// kickEventKicksGifted fires when a viewer spends Kicks (Kick's
	// virtual currency) in the channel — the Kick equivalent of a
	// Twitch Cheer/Bits.
	kickEventKicksGifted = "kicks.gifted"
	kickEventVersion     = 1
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
	ID                string `json:"id"`
	Event             string `json:"event"`
	BroadcasterUserID int64  `json:"broadcaster_user_id"`
}

// kickEventsAPIRequest performs a Kick /events/subscriptions request the
// same way kickAPIRequest does, but additionally logs the request body
// (if any) and the full raw response body. Deliberately kept separate
// from kickAPIRequest rather than added there: that helper is shared by
// every Kick API call this app makes, including ones that return things
// like a viewer's email address (/users) that have no business being
// dumped to the log — this is only for subscription management, where
// seeing exactly what was sent and what Kick sent back is worth it for
// debugging registration issues.
func kickEventsAPIRequest(ctx context.Context, method, endpoint, accessToken string, body []byte, out any) error {
	if len(body) > 0 {
		log.Printf("kick: %s %s request body: %s", method, endpoint, body)
	}

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("kick: %s %s request failed: %v", method, endpoint, err)
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("kick: %s %s: could not read response body: %v", method, endpoint, err)
		return err
	}
	log.Printf("kick: %s %s response (%s): %s", method, endpoint, resp.Status, data)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &envelope)
		if envelope.Message == "" {
			envelope.Message = resp.Status
		}
		return &kickAPIError{Status: resp.StatusCode, Message: envelope.Message}
	}

	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

// kickAppTokenCache lazily fetches and caches an app-level Kick access
// token (see fetchKickAppAccessToken), refreshing it once it's expired.
// Webhook subscription management needs this instead of a signed-in
// viewer's own access token: per Kick's docs, a user token's
// broadcaster_user_id in the POST body is silently ignored in favor of
// whichever broadcaster the token itself belongs to. Subscribing with a
// moderator's own token would then only ever succeed for their own
// channel — never the one they're actually trying to watch — which is
// exactly the "wrong ID" symptom this was written to fix.
// https://docs.kick.com/events/subscribe-to-events
type kickAppTokenCache struct {
	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func (c *kickAppTokenCache) get(ctx context.Context, cfg Config) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.expiresAt) {
		return c.token, nil
	}

	tok, err := fetchKickAppAccessToken(ctx, cfg)
	if err != nil {
		return "", err
	}
	c.token = tok.AccessToken
	// Refresh a little before actual expiry so a call in flight doesn't
	// race a token that's about to stop working.
	c.expiresAt = kickTokenExpiry(tok).Add(-30 * time.Second)
	return c.token, nil
}

// ensureKickEventSubscription makes sure broadcasterUserID has an active
// webhook subscription for eventName, creating one if not, always using
// an app-level access token (see kickAppTokenCache above — never the
// signed-in viewer's own). Duplicate subscriptions would make Kick
// deliver every event twice, so this checks first rather than always
// posting; since listing subscriptions returns every one this app has
// across every broadcaster (not just broadcasterUserID's), that check
// filters on both the event name and the broadcaster, not just the
// event name — otherwise the very first channel this app subscribes to
// would make every other channel look "already subscribed" too.
// Registering a new subscription (or failing to) is logged — the
// already-subscribed case isn't, since this runs on every chat
// connection and would otherwise log the same "nothing to do" outcome
// on every reconnect.
func (s *Server) ensureKickEventSubscription(ctx context.Context, broadcasterUserID int64, eventName string) error {
	accessToken, err := s.kickAppToken.get(ctx, s.cfg)
	if err != nil {
		return fmt.Errorf("fetching app access token: %w", err)
	}

	var existing struct {
		Data []kickEventSubscription `json:"data"`
	}
	listURL := kickAPIBase + "/events/subscriptions"
	if err := kickEventsAPIRequest(ctx, http.MethodGet, listURL, accessToken, nil, &existing); err != nil {
		log.Printf("kick: could not list event subscriptions for broadcaster %d (checking %s): %v", broadcasterUserID, eventName, err)
		return err
	}
	for _, sub := range existing.Data {
		if sub.Event == eventName && sub.BroadcasterUserID == broadcasterUserID {
			return nil
		}
	}

	body, err := json.Marshal(struct {
		BroadcasterUserID int64 `json:"broadcaster_user_id"`
		Events            []struct {
			Name    string `json:"name"`
			Version int    `json:"version"`
		} `json:"events"`
		Method string `json:"method"`
	}{
		BroadcasterUserID: broadcasterUserID,
		Events: []struct {
			Name    string `json:"name"`
			Version int    `json:"version"`
		}{{Name: eventName, Version: kickEventVersion}},
		Method: "webhook",
	})
	if err != nil {
		return err
	}
	if err := kickEventsAPIRequest(ctx, http.MethodPost, kickAPIBase+"/events/subscriptions", accessToken, body, nil); err != nil {
		log.Printf("kick: could not subscribe broadcaster %d to %s: %v", broadcasterUserID, eventName, err)
		return err
	}
	log.Printf("kick: subscribed broadcaster %d to %s", broadcasterUserID, eventName)
	return nil
}

// clearKickWebhookSubscriptions deletes every webhook subscription
// currently registered for this app on Kick — across every broadcaster,
// not just ones this process happens to know about — using an app-level
// access token rather than any particular viewer's, since this is meant
// to run at startup before anyone has signed in. It guards against
// subscriptions accumulating across restarts: ensureKickEventSubscription
// only ever adds a subscription for a channel once someone connects to
// its chat, so a previous run's subscriptions would otherwise sit there
// forever, for channels this run may never even touch.
func clearKickWebhookSubscriptions(ctx context.Context, cfg Config) error {
	tok, err := fetchKickAppAccessToken(ctx, cfg)
	if err != nil {
		return fmt.Errorf("fetching app access token: %w", err)
	}

	var existing struct {
		Data []kickEventSubscription `json:"data"`
	}
	if err := kickEventsAPIRequest(ctx, http.MethodGet, kickAPIBase+"/events/subscriptions", tok.AccessToken, nil, &existing); err != nil {
		return fmt.Errorf("listing subscriptions: %w", err)
	}
	if len(existing.Data) == 0 {
		return nil
	}

	params := url.Values{}
	for _, sub := range existing.Data {
		params.Add("id", sub.ID)
	}
	endpoint := kickAPIBase + "/events/subscriptions?" + params.Encode()
	if err := kickEventsAPIRequest(ctx, http.MethodDelete, endpoint, tok.AccessToken, nil, nil); err != nil {
		return fmt.Errorf("deleting %d subscription(s): %w", len(existing.Data), err)
	}

	log.Printf("kick: cleared %d stale webhook subscription(s) on startup", len(existing.Data))
	return nil
}

// removeKickEventSubscriptionsForBroadcaster deletes every webhook
// subscription this app currently has for broadcasterUserID. Called
// once the last viewer watching that channel disconnects (see
// handleKickChatStream) — kickHub lets several viewers of the same
// channel share one set of Kick webhook subscriptions, so this only
// runs when nobody's left watching it, not on every individual
// disconnect. Best-effort: failures are logged, not propagated, since
// there's no request left to fail by the time this runs, and a
// subscription left behind here is still cleaned up eventually by
// clearKickWebhookSubscriptions on the next startup.
func (s *Server) removeKickEventSubscriptionsForBroadcaster(ctx context.Context, broadcasterUserID int64) {
	accessToken, err := s.kickAppToken.get(ctx, s.cfg)
	if err != nil {
		log.Printf("kick: could not fetch app access token to remove broadcaster %d's subscriptions: %v", broadcasterUserID, err)
		return
	}

	var existing struct {
		Data []kickEventSubscription `json:"data"`
	}
	if err := kickEventsAPIRequest(ctx, http.MethodGet, kickAPIBase+"/events/subscriptions", accessToken, nil, &existing); err != nil {
		log.Printf("kick: could not list event subscriptions to remove broadcaster %d's: %v", broadcasterUserID, err)
		return
	}

	var ids []string
	for _, sub := range existing.Data {
		if sub.BroadcasterUserID == broadcasterUserID {
			ids = append(ids, sub.ID)
		}
	}
	if len(ids) == 0 {
		return
	}

	// Re-check right before deleting: a viewer may have reconnected to
	// this same channel in the time since the last one left (e.g.
	// switching away and immediately back), in which case these
	// subscriptions are wanted again and shouldn't be torn down.
	if s.kickHub.subscriberCount(broadcasterUserID) > 0 {
		log.Printf("kick: skipping subscription removal for broadcaster %d — a viewer reconnected", broadcasterUserID)
		return
	}

	params := url.Values{}
	for _, id := range ids {
		params.Add("id", id)
	}
	endpoint := kickAPIBase + "/events/subscriptions?" + params.Encode()
	if err := kickEventsAPIRequest(ctx, http.MethodDelete, endpoint, accessToken, nil, nil); err != nil {
		log.Printf("kick: could not remove %d subscription(s) for broadcaster %d: %v", len(ids), broadcasterUserID, err)
		return
	}
	log.Printf("kick: removed %d webhook subscription(s) for broadcaster %d (no viewers left watching)", len(ids), broadcasterUserID)
}

// --- Fan-out ---------------------------------------------------------------

// kickBadge and kickIdentity describe a chatter's badges (moderator,
// broadcaster, subscriber, ...), used both to decide which moderation
// controls are safe to show for a message (mirrors how the Twitch
// integration reads Mod/Broadcaster off EventSub badges) and to render the
// actual badge icons next to their name. Count is set for tiered badges
// (e.g. subscriber tenure).
type kickBadge struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
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
	MessageID string `json:"message_id"`
	// RepliesTo is set when this message is a threaded reply to an
	// earlier one; nil otherwise.
	RepliesTo   *kickReplyRef `json:"replies_to"`
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

// kickReplyRef identifies the parent message a chat.message.sent event
// is threaded to.
type kickReplyRef struct {
	MessageID string `json:"message_id"`
	Content   string `json:"content"`
	Sender    struct {
		Username string `json:"username"`
	} `json:"sender"`
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

// kickSubscriptionGiftsEvent is the channel.subscription.gifts webhook
// payload. Only the fields this app actually renders are parsed — the
// gifter and how many giftees there were, not each giftee's identity.
// https://docs.kick.com/events/event-types
type kickSubscriptionGiftsEvent struct {
	Broadcaster struct {
		UserID int64 `json:"user_id"`
	} `json:"broadcaster"`
	Gifter struct {
		IsAnonymous bool   `json:"is_anonymous"`
		Username    string `json:"username"`
	} `json:"gifter"`
	// Giftees is only ever used for its length (how many subs were
	// gifted) — see writeKickStreamEvent.
	Giftees []struct {
		UserID int64 `json:"user_id"`
	} `json:"giftees"`
}

// kickKicksGiftedEvent is the kicks.gifted webhook payload: a viewer
// spent Kicks (Kick's virtual currency) in the channel, similar to a
// Twitch Cheer/Bits. Unlike gift subs, Kick documents the sender as
// never anonymous for this event.
// https://docs.kick.com/events/event-types
type kickKicksGiftedEvent struct {
	Broadcaster struct {
		UserID int64 `json:"user_id"`
	} `json:"broadcaster"`
	Sender struct {
		Username string `json:"username"`
	} `json:"sender"`
	Gift struct {
		Amount int    `json:"amount"`
		Name   string `json:"name"`
		// Message is the optional note the sender attached to the gift.
		Message string `json:"message"`
	} `json:"gift"`
}

// kickStreamEvent tags which of the four payloads a fan-out subscriber
// received, mirroring streamEvent in handlers_chat.go.
type kickStreamEvent struct {
	kind        string // "message", "banned", "giftsub", or "kicksgifted"
	message     kickChatMessageEvent
	banned      kickModerationBannedEvent
	giftSubs    kickSubscriptionGiftsEvent
	kicksGifted kickKicksGiftedEvent
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

// subscribe registers a new viewer for broadcasterUserID. The returned
// unsubscribe function reports whether it just removed the last
// remaining viewer for that broadcaster — callers use that to decide
// whether it's time to tear down the underlying Kick webhook
// subscriptions too (see handleKickChatStream), since this hub is what
// lets several viewers of the same channel share one set of them.
func (h *kickHub) subscribe(broadcasterUserID int64) (ch chan kickStreamEvent, unsubscribe func() (wasLast bool)) {
	ch = make(chan kickStreamEvent, 64)

	h.mu.Lock()
	if h.subscribers[broadcasterUserID] == nil {
		h.subscribers[broadcasterUserID] = make(map[chan kickStreamEvent]struct{})
	}
	h.subscribers[broadcasterUserID][ch] = struct{}{}
	h.mu.Unlock()

	return ch, func() (wasLast bool) {
		h.mu.Lock()
		delete(h.subscribers[broadcasterUserID], ch)
		wasLast = len(h.subscribers[broadcasterUserID]) == 0
		if wasLast {
			delete(h.subscribers, broadcasterUserID)
		}
		h.mu.Unlock()
		close(ch)
		return wasLast
	}
}

// subscriberCount reports how many viewers are currently watching
// broadcasterUserID. Used to re-check, right before actually deleting a
// Kick webhook subscription, that nobody's reconnected to that channel
// in the (brief, best-effort) window since the last viewer first left —
// see removeKickEventSubscriptionsForBroadcaster.
func (h *kickHub) subscriberCount(broadcasterUserID int64) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subscribers[broadcasterUserID])
}

// publish delivers ev to every current subscriber for broadcasterUserID.
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
			s.kickChatHistory.add(msg.Broadcaster.UserID, kickChatMessageDTOFromEvent(msg.Broadcaster.ChannelSlug, msg))
		}
	case kickEventModerationBans:
		var banned kickModerationBannedEvent
		if err := json.Unmarshal(body, &banned); err == nil {
			s.kickHub.publish(banned.Broadcaster.UserID, kickStreamEvent{kind: "banned", banned: banned})
		}
	case kickEventSubscriptionGifts:
		var gift kickSubscriptionGiftsEvent
		if err := json.Unmarshal(body, &gift); err == nil {
			s.kickHub.publish(gift.Broadcaster.UserID, kickStreamEvent{kind: "giftsub", giftSubs: gift})
		}
	case kickEventKicksGifted:
		var kicks kickKicksGiftedEvent
		if err := json.Unmarshal(body, &kicks); err == nil {
			s.kickHub.publish(kicks.Broadcaster.UserID, kickStreamEvent{kind: "kicksgifted", kicksGifted: kicks})
		}
	}

	w.WriteHeader(http.StatusOK)
}
