package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// --- OAuth ---------------------------------------------------------------

func (s *Server) handleKickLogin(w http.ResponseWriter, r *http.Request) {
	if s.cfg.KickClientID == "" || s.cfg.KickClientSecret == "" {
		http.Error(w, "kick OAuth is not configured on the server", http.StatusServiceUnavailable)
		return
	}

	returnTo := r.URL.Query().Get("return_to")
	if returnTo == "" || !isSafeReturnTo(returnTo) {
		returnTo = "/"
	}

	verifier, challenge, err := generatePKCE()
	if err != nil {
		http.Error(w, "failed to start kick login", http.StatusInternalServerError)
		return
	}

	state, err := s.kickAuth.putState(returnTo, verifier)
	if err != nil {
		http.Error(w, "failed to start kick login", http.StatusInternalServerError)
		return
	}

	params := url.Values{
		"client_id":             {s.cfg.KickClientID},
		"redirect_uri":          {s.cfg.KickRedirectURL},
		"response_type":         {"code"},
		"scope":                 {kickOAuthScopes},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	authorizeURL := kickAuthorizeURL + "?" + params.Encode()
	log.Printf("kick: redirecting to authorize URL: %s", authorizeURL)
	http.Redirect(w, r, authorizeURL, http.StatusFound)
}

func (s *Server) handleKickCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	entry, ok := s.kickAuth.takeState(q.Get("state"))
	if !ok {
		http.Error(w, "invalid or expired login attempt, please try again", http.StatusBadRequest)
		return
	}

	if msg := q.Get("error_description"); msg != "" {
		redirectWithError(w, r, entry.returnTo, msg)
		return
	}

	code := q.Get("code")
	if code == "" {
		redirectWithError(w, r, entry.returnTo, "kick did not return an authorization code")
		return
	}

	ctx := r.Context()
	tok, err := exchangeKickCode(ctx, s.cfg, code, entry.codeVerifier)
	if err != nil {
		redirectWithError(w, r, entry.returnTo, "could not complete kick login")
		return
	}

	user, err := fetchKickUser(ctx, tok.AccessToken)
	if err != nil {
		redirectWithError(w, r, entry.returnTo, "could not load kick profile")
		return
	}

	sess := &kickSession{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		UserID:       user.UserID,
		Username:     user.Name,
		ProfileImage: user.ProfilePicture,
		ExpiresAt:    kickTokenExpiry(tok),
	}
	sessionToken, err := s.kickAuth.putSession(sess)
	if err != nil {
		redirectWithError(w, r, entry.returnTo, "could not start session")
		return
	}

	s.setKickSessionCookie(w, sessionToken)
	http.Redirect(w, r, entry.returnTo, http.StatusFound)
}

type kickSessionUser struct {
	Username        string `json:"username"`
	ProfileImageURL string `json:"profileImageUrl"`
}

type kickSessionResponse struct {
	Authenticated bool             `json:"authenticated"`
	User          *kickSessionUser `json:"user,omitempty"`
}

func (s *Server) handleKickAuthSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	sess := s.kickSessionFromRequest(r)
	if sess == nil {
		json.NewEncoder(w).Encode(kickSessionResponse{Authenticated: false})
		return
	}
	json.NewEncoder(w).Encode(kickSessionResponse{
		Authenticated: true,
		User: &kickSessionUser{
			Username:        sess.Username,
			ProfileImageURL: sess.ProfileImage,
		},
	})
}

func (s *Server) handleKickAuthLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(kickSessionCookieName); err == nil {
		s.kickAuth.deleteSession(c.Value)
	}
	s.clearKickSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// handleKickOwnChannel returns the signed-in user's own channel, offered
// as a one-click pick in the UI since Kick has no "channels I moderate"
// listing endpoint the way Twitch does.
func (s *Server) handleKickOwnChannel(w http.ResponseWriter, r *http.Request) {
	sess := s.kickSessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	channel, err := fetchOwnKickChannel(r.Context(), sess.AccessToken)
	if err != nil {
		http.Error(w, "failed to load kick channel", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Slug string `json:"slug"`
	}{Slug: channel.Slug})
}

// --- Moderation ------------------------------------------------------------

func (s *Server) handleDeleteKickChatMessage(w http.ResponseWriter, r *http.Request) {
	sess := s.kickSessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	messageID := strings.TrimSpace(r.URL.Query().Get("messageId"))
	if messageID == "" {
		http.Error(w, "messageId query parameter is required", http.StatusBadRequest)
		return
	}

	if err := deleteKickChatMessage(r.Context(), sess.AccessToken, messageID); err != nil {
		writeKickAPIError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type kickSendMessageRequestDTO struct {
	Channel string `json:"channel"`
	Message string `json:"message"`
	// ReplyTo is the message ID being replied to, if any.
	ReplyTo string `json:"replyTo,omitempty"`
}

// handleSendKickChatMessage posts a new chat message to the channel,
// sent as the signed-in user themselves.
func (s *Server) handleSendKickChatMessage(w http.ResponseWriter, r *http.Request) {
	sess := s.kickSessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	var req kickSendMessageRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}
	channel, err := normalizeKickChannel(req.Channel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	broadcasterID, err := s.resolveKickBroadcasterID(r.Context(), sess.AccessToken, channel)
	if err != nil {
		http.Error(w, "could not resolve channel", http.StatusBadGateway)
		return
	}

	if err := sendKickChatMessage(r.Context(), sess.AccessToken, broadcasterID, message, strings.TrimSpace(req.ReplyTo)); err != nil {
		writeKickAPIError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type kickBanRequestDTO struct {
	Channel string `json:"channel"`
	UserID  int64  `json:"userId"`
	Reason  string `json:"reason,omitempty"`
}

type kickTimeoutRequestDTO struct {
	Channel         string `json:"channel"`
	UserID          int64  `json:"userId"`
	Reason          string `json:"reason,omitempty"`
	DurationMinutes int    `json:"durationMinutes"`
}

// handleBanKickUser permanently bans a chatter from the channel.
func (s *Server) handleBanKickUser(w http.ResponseWriter, r *http.Request) {
	sess := s.kickSessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	var req kickBanRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.UserID == 0 {
		http.Error(w, "userId is required", http.StatusBadRequest)
		return
	}
	channel, err := normalizeKickChannel(req.Channel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	broadcasterID, err := s.resolveKickBroadcasterID(r.Context(), sess.AccessToken, channel)
	if err != nil {
		http.Error(w, "could not resolve channel", http.StatusBadGateway)
		return
	}

	if err := banOrTimeoutKickUser(r.Context(), sess.AccessToken, broadcasterID, req.UserID, 0, req.Reason); err != nil {
		writeKickAPIError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTimeoutKickUser temporarily bans (times out) a chatter. Kick
// takes the duration in minutes (1-10080), unlike Twitch's seconds.
func (s *Server) handleTimeoutKickUser(w http.ResponseWriter, r *http.Request) {
	sess := s.kickSessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	var req kickTimeoutRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.UserID == 0 {
		http.Error(w, "userId is required", http.StatusBadRequest)
		return
	}
	if req.DurationMinutes <= 0 || req.DurationMinutes > 10080 {
		http.Error(w, "durationMinutes must be between 1 and 10080", http.StatusBadRequest)
		return
	}
	channel, err := normalizeKickChannel(req.Channel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	broadcasterID, err := s.resolveKickBroadcasterID(r.Context(), sess.AccessToken, channel)
	if err != nil {
		http.Error(w, "could not resolve channel", http.StatusBadGateway)
		return
	}

	if err := banOrTimeoutKickUser(r.Context(), sess.AccessToken, broadcasterID, req.UserID, req.DurationMinutes, req.Reason); err != nil {
		writeKickAPIError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeKickAPIError(w http.ResponseWriter, err error) {
	if apiErr, ok := err.(*kickAPIError); ok {
		status := apiErr.Status
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		http.Error(w, apiErr.Message, status)
		return
	}
	http.Error(w, "kick moderation request failed", http.StatusBadGateway)
}

// --- Chat stream -----------------------------------------------------------

type kickChatMessageDTO struct {
	ID          string         `json:"id"`
	Channel     string         `json:"channel"`
	Text        string         `json:"text"`
	Username    string         `json:"username"`
	UserID      int64          `json:"userId"`
	Color       string         `json:"color"`
	Mod         bool           `json:"mod"`
	Subscriber  bool           `json:"subscriber"`
	Broadcaster bool           `json:"broadcaster"`
	Badges      []chatBadgeDTO `json:"badges"`
	// Reply is the parent message this one is replying to, if any (see
	// chatReplyDTO in handlers_chat.go).
	Reply *chatReplyDTO `json:"reply,omitempty"`
	// Fragments breaks Text down for rendering, substituting an inline
	// emote image for each fragment that has one (see chatFragmentDTO
	// in handlers_chat.go). Omitted for a message with no emotes.
	Fragments []chatFragmentDTO `json:"fragments,omitempty"`
	Timestamp int64             `json:"timestamp"`
}

// kickBadgeDTOs converts a chatter's Kick badges to the shared DTO
// shape used for both Twitch and Kick messages (see chatBadgeDTO in
// handlers_chat.go).
func kickBadgeDTOs(identity *kickIdentity) []chatBadgeDTO {
	if identity == nil {
		return nil
	}
	out := make([]chatBadgeDTO, len(identity.Badges))
	for i, b := range identity.Badges {
		dto := chatBadgeDTO{Name: b.Type}
		if b.Count > 0 {
			dto.Version = strconv.Itoa(b.Count)
		}
		out[i] = dto
	}
	return out
}

// kickEmoteFragmentPattern matches Kick's inline emote placeholder
// syntax embedded directly in a message's content, e.g.
// "[emote:39262:Prayge]" — unlike Twitch's EventSub, which
// pre-segments a message into fragments, Kick leaves emotes as tokens
// inside the plain text.
var kickEmoteFragmentPattern = regexp.MustCompile(`\[emote:(\d+):([^\]]*)\]`)

// kickEmoteImageURL builds an emote's CDN image URL from its ID.
func kickEmoteImageURL(emoteID string) string {
	return "https://files.kick.com/emotes/" + emoteID + "/fullsize"
}

// kickMessageFragments splits content into a mix of plain-text and
// emote fragments by replacing each [emote:id:name] token with an
// image fragment (name becomes its alt/title text). Returns nil for
// content with no emote tokens, so an ordinary message needs no
// fragment breakdown.
func kickMessageFragments(content string) []chatFragmentDTO {
	matches := kickEmoteFragmentPattern.FindAllStringSubmatchIndex(content, -1)
	if matches == nil {
		return nil
	}

	var out []chatFragmentDTO
	last := 0
	for _, m := range matches {
		start, end := m[0], m[1]
		if start > last {
			out = append(out, chatFragmentDTO{Text: content[last:start]})
		}
		id, name := content[m[2]:m[3]], content[m[4]:m[5]]
		out = append(out, chatFragmentDTO{Text: name, ImageURL: kickEmoteImageURL(id)})
		last = end
	}
	if last < len(content) {
		out = append(out, chatFragmentDTO{Text: content[last:]})
	}
	return out
}

type kickBannedDTO struct {
	Channel         string `json:"channel"`
	UserID          int64  `json:"userId"`
	Username        string `json:"username"`
	DurationMinutes int    `json:"durationMinutes,omitempty"`
}

// handleKickChatStream streams a channel's chat to the browser over SSE.
// Unlike the Twitch equivalent, this requires the viewer to be signed
// in — Kick's public API has no anonymous way to read chat, only
// webhooks delivered to a subscription tied to a user's token — and
// depends on the app's webhook URL (configured once in the Kick
// developer dashboard, see README) actually being reachable by Kick.
func (s *Server) handleKickChatStream(w http.ResponseWriter, r *http.Request) {
	sess := s.kickSessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	channel, err := normalizeKickChannel(r.URL.Query().Get("channel"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	broadcasterID, err := s.resolveKickBroadcasterID(ctx, sess.AccessToken, channel)
	if err != nil {
		http.Error(w, "could not resolve channel", http.StatusBadGateway)
		return
	}

	if err := s.ensureKickSubscriptions(ctx, broadcasterID); err != nil {
		writeKickAPIError(w, err)
		return
	}

	events, unsubscribe := s.kickHub.subscribe(broadcasterID)
	defer func() {
		if !unsubscribe() {
			// Other viewers are still watching this channel — its
			// webhook subscriptions stay in place for them.
			return
		}
		// Best-effort, off the request's own context (which is done by
		// the time this runs) so a slow Kick API doesn't hold anything
		// open, and off the request's goroutine so this viewer's
		// disconnect isn't held up by it either.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			s.removeKickEventSubscriptionsForBroadcaster(ctx, broadcasterID)
		}()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Kick's webhooks are push-only — there's no way to fetch messages
	// posted before this subscription started — so the best available
	// "history" is whatever this server has already seen for this
	// channel since it started up.
	for _, dto := range s.kickChatHistory.recent(broadcasterID) {
		writeSSE(w, "message", dto)
	}
	flusher.Flush()

	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			writeKickStreamEvent(w, channel, ev)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// ensureKickSubscriptions makes sure the app is subscribed to the
// webhook events it cares about for broadcasterID. Only
// chat.message.sent is treated as essential here — moderation.banned
// and channel.subscription.gifts add ban/timeout and gift-sub notices
// on top of the core chat feed, but a failure subscribing to either of
// those (e.g. an event type this app isn't approved for) shouldn't
// take the whole chat stream down with it.
func (s *Server) ensureKickSubscriptions(ctx context.Context, broadcasterID int64) error {
	if err := s.ensureKickEventSubscription(ctx, broadcasterID, kickEventMessageSent); err != nil {
		return err
	}
	_ = s.ensureKickEventSubscription(ctx, broadcasterID, kickEventModerationBans)
	_ = s.ensureKickEventSubscription(ctx, broadcasterID, kickEventSubscriptionGifts)
	return nil
}

// kickChatMessageDTOFromEvent builds the DTO sent to the browser for a
// chat.message.sent webhook event. It's also used to record history
// (see Server.kickChatHistory) at the point the webhook is received,
// rather than per-viewer in writeKickStreamEvent, since a channel with
// several viewers connected would otherwise record the same message
// once per viewer.
func kickChatMessageDTOFromEvent(channel string, m kickChatMessageEvent) kickChatMessageDTO {
	identity := m.Sender.Identity
	ts := time.Now().UnixMilli()
	if parsed, err := time.Parse(time.RFC3339, m.CreatedAt); err == nil {
		ts = parsed.UnixMilli()
	}
	color := ""
	if identity != nil {
		color = identity.UsernameColor
	}
	dto := kickChatMessageDTO{
		ID:          m.MessageID,
		Channel:     channel,
		Text:        m.Content,
		Username:    m.Sender.Username,
		UserID:      m.Sender.UserID,
		Color:       color,
		Mod:         identity.hasBadge("moderator"),
		Subscriber:  identity.hasBadge("subscriber"),
		Broadcaster: identity.hasBadge("broadcaster"),
		Badges:      kickBadgeDTOs(identity),
		Fragments:   kickMessageFragments(m.Content),
		Timestamp:   ts,
	}
	if m.RepliesTo != nil {
		dto.Reply = &chatReplyDTO{
			Username: m.RepliesTo.Sender.Username,
			Text:     m.RepliesTo.Content,
		}
	}
	return dto
}

func writeKickStreamEvent(w http.ResponseWriter, channel string, ev kickStreamEvent) {
	switch ev.kind {
	case "message":
		writeSSE(w, "message", kickChatMessageDTOFromEvent(channel, ev.message))
	case "banned":
		b := ev.banned
		dto := kickBannedDTO{
			Channel:  channel,
			UserID:   b.BannedUser.UserID,
			Username: b.BannedUser.Username,
		}
		if b.Metadata.ExpiresAt != nil {
			if expires, err := time.Parse(time.RFC3339, *b.Metadata.ExpiresAt); err == nil {
				if mins := int(time.Until(expires).Minutes()); mins > 0 {
					dto.DurationMinutes = mins
				}
			}
		}
		writeSSE(w, "banned", dto)
	case "giftsub":
		g := ev.giftSubs
		name := g.Gifter.Username
		if g.Gifter.IsAnonymous || name == "" {
			name = "An anonymous gifter"
		}
		writeSSE(w, "giftsub", giftSubDTO{
			Channel:     channel,
			DisplayName: name,
			Count:       len(g.Giftees),
		})
	}
}
