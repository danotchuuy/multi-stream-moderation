package server

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// --- OAuth ---------------------------------------------------------------

func (s *Server) handleYouTubeLogin(w http.ResponseWriter, r *http.Request) {
	if s.cfg.YouTubeClientID == "" || s.cfg.YouTubeClientSecret == "" {
		http.Error(w, "youtube OAuth is not configured on the server", http.StatusServiceUnavailable)
		return
	}

	returnTo := r.URL.Query().Get("return_to")
	if returnTo == "" || !isSafeReturnTo(returnTo) {
		returnTo = "/"
	}

	state, err := s.youtubeAuth.putState(returnTo)
	if err != nil {
		http.Error(w, "failed to start youtube login", http.StatusInternalServerError)
		return
	}

	params := url.Values{
		"client_id":     {s.cfg.YouTubeClientID},
		"redirect_uri":  {s.cfg.YouTubeRedirectURL},
		"response_type": {"code"},
		"scope":         {youtubeOAuthScope},
		"state":         {state},
		// access_type=offline + prompt=consent: without both, Google
		// only issues a refresh_token the very first time an account
		// ever consents, so a later reauth here (needed after e.g.
		// deleting the session file) would silently come back with no
		// refresh_token and quietly break persistence across restarts.
		"access_type": {"offline"},
		"prompt":      {"consent"},
	}
	http.Redirect(w, r, youtubeAuthorizeURL+"?"+params.Encode(), http.StatusFound)
}

func (s *Server) handleYouTubeCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	returnTo, ok := s.youtubeAuth.takeState(q.Get("state"))
	if !ok {
		http.Error(w, "invalid or expired login attempt, please try again", http.StatusBadRequest)
		return
	}

	if msg := q.Get("error_description"); msg != "" {
		redirectWithError(w, r, returnTo, "youtube_error", msg)
		return
	}
	if msg := q.Get("error"); msg != "" {
		redirectWithError(w, r, returnTo, "youtube_error", msg)
		return
	}

	code := q.Get("code")
	if code == "" {
		redirectWithError(w, r, returnTo, "youtube_error", "youtube did not return an authorization code")
		return
	}

	ctx := r.Context()
	tok, err := exchangeYouTubeCode(ctx, s.cfg, code)
	if err != nil {
		log.Printf("youtube: token exchange failed: %v", err)
		redirectWithError(w, r, returnTo, "youtube_error", "could not complete youtube login")
		return
	}

	channel, err := fetchOwnYouTubeChannel(ctx, tok.AccessToken)
	if err != nil {
		log.Printf("youtube: could not load channel after login: %v", err)
		redirectWithError(w, r, returnTo, "youtube_error", "could not load youtube channel")
		return
	}

	sess := &youtubeSession{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ChannelID:    channel.ID,
		ChannelTitle: channel.Title,
		ProfileImage: channel.Image,
		ExpiresAt:    youtubeTokenExpiry(tok),
	}
	sessionToken, err := s.youtubeAuth.putSession(sess)
	if err != nil {
		redirectWithError(w, r, returnTo, "youtube_error", "could not start session")
		return
	}

	s.setYouTubeSessionCookie(w, sessionToken)
	http.Redirect(w, r, returnTo, http.StatusFound)
}

type youtubeSessionUser struct {
	ChannelID       string `json:"channelId"`
	ChannelTitle    string `json:"channelTitle"`
	ProfileImageURL string `json:"profileImageUrl"`
}

type youtubeSessionResponse struct {
	Authenticated bool                `json:"authenticated"`
	User          *youtubeSessionUser `json:"user,omitempty"`
}

func (s *Server) handleYouTubeAuthSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	sess := s.youtubeSessionFromRequest(r)
	if sess == nil {
		json.NewEncoder(w).Encode(youtubeSessionResponse{Authenticated: false})
		return
	}
	json.NewEncoder(w).Encode(youtubeSessionResponse{
		Authenticated: true,
		User: &youtubeSessionUser{
			ChannelID:       sess.ChannelID,
			ChannelTitle:    sess.ChannelTitle,
			ProfileImageURL: sess.ProfileImage,
		},
	})
}

func (s *Server) handleYouTubeAuthLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(youtubeSessionCookieName); err == nil {
		s.youtubeAuth.deleteSession(c.Value)
	}
	s.clearYouTubeSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// --- Picking a live chat ---------------------------------------------------

type youtubeLiveChatRefDTO struct {
	LiveChatID   string `json:"liveChatId"`
	ChannelID    string `json:"channelId"`
	ChannelTitle string `json:"channelTitle"`
}

// handleOwnYouTubeLiveChat finds the signed-in user's own currently
// active broadcast, offered as a one-click pick — like Kick, YouTube has
// no "channels I moderate" listing, and unlike Kick a channel doesn't
// even have a single persistent chat: only individual broadcasts do.
func (s *Server) handleOwnYouTubeLiveChat(w http.ResponseWriter, r *http.Request) {
	sess := s.youtubeSessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	liveChatID, err := resolveOwnActiveLiveChat(r.Context(), sess.AccessToken)
	if err != nil {
		writeYouTubeError(w, err, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(youtubeLiveChatRefDTO{
		LiveChatID:   liveChatID,
		ChannelID:    sess.ChannelID,
		ChannelTitle: sess.ChannelTitle,
	})
}

// handleResolveYouTubeVideo resolves a pasted video ID or URL to its
// live chat ID.
func (s *Server) handleResolveYouTubeVideo(w http.ResponseWriter, r *http.Request) {
	sess := s.youtubeSessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	raw := strings.TrimSpace(r.URL.Query().Get("video"))
	if raw == "" {
		http.Error(w, "video query parameter is required", http.StatusBadRequest)
		return
	}
	videoID, err := parseYouTubeVideoID(raw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	liveChatID, channelID, channelTitle, err := resolveVideoLiveChat(r.Context(), sess.AccessToken, videoID)
	if err != nil {
		writeYouTubeError(w, err, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(youtubeLiveChatRefDTO{
		LiveChatID:   liveChatID,
		ChannelID:    channelID,
		ChannelTitle: channelTitle,
	})
}

func writeYouTubeError(w http.ResponseWriter, err error, fallbackStatus int) {
	if apiErr, ok := err.(*youtubeAPIError); ok {
		status := apiErr.Status
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		http.Error(w, apiErr.Message, status)
		return
	}
	http.Error(w, err.Error(), fallbackStatus)
}

// --- Moderation ------------------------------------------------------------

func (s *Server) handleDeleteYouTubeChatMessage(w http.ResponseWriter, r *http.Request) {
	sess := s.youtubeSessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	messageID := strings.TrimSpace(r.URL.Query().Get("messageId"))
	if messageID == "" {
		http.Error(w, "messageId query parameter is required", http.StatusBadRequest)
		return
	}

	if err := deleteYouTubeChatMessage(r.Context(), sess.AccessToken, messageID); err != nil {
		writeYouTubeError(w, err, http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type youtubeSendMessageRequestDTO struct {
	LiveChatID string `json:"liveChatId"`
	Message    string `json:"message"`
}

// handleSendYouTubeChatMessage posts a new text message to the live
// chat, sent as the signed-in user.
func (s *Server) handleSendYouTubeChatMessage(w http.ResponseWriter, r *http.Request) {
	sess := s.youtubeSessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	var req youtubeSendMessageRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}
	if req.LiveChatID == "" {
		http.Error(w, "liveChatId is required", http.StatusBadRequest)
		return
	}

	sent, err := sendYouTubeChatMessage(r.Context(), sess.AccessToken, req.LiveChatID, message)
	if err != nil {
		writeYouTubeError(w, err, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sent)
}

type youtubeBanRequestDTO struct {
	LiveChatID string `json:"liveChatId"`
	ChannelID  string `json:"channelId"`
}

type youtubeTimeoutRequestDTO struct {
	LiveChatID      string `json:"liveChatId"`
	ChannelID       string `json:"channelId"`
	DurationSeconds int    `json:"durationSeconds"`
}

// handleBanYouTubeUser permanently bans a chatter from the live chat.
func (s *Server) handleBanYouTubeUser(w http.ResponseWriter, r *http.Request) {
	sess := s.youtubeSessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	var req youtubeBanRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.LiveChatID == "" || req.ChannelID == "" {
		http.Error(w, "liveChatId and channelId are required", http.StatusBadRequest)
		return
	}

	if err := banOrTimeoutYouTubeUser(r.Context(), sess.AccessToken, req.LiveChatID, req.ChannelID, 0); err != nil {
		writeYouTubeError(w, err, http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTimeoutYouTubeUser temporarily bans (times out) a chatter.
func (s *Server) handleTimeoutYouTubeUser(w http.ResponseWriter, r *http.Request) {
	sess := s.youtubeSessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	var req youtubeTimeoutRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.LiveChatID == "" || req.ChannelID == "" {
		http.Error(w, "liveChatId and channelId are required", http.StatusBadRequest)
		return
	}
	if req.DurationSeconds <= 0 {
		http.Error(w, "durationSeconds must be greater than zero", http.StatusBadRequest)
		return
	}

	if err := banOrTimeoutYouTubeUser(r.Context(), sess.AccessToken, req.LiveChatID, req.ChannelID, req.DurationSeconds); err != nil {
		writeYouTubeError(w, err, http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Chat stream -----------------------------------------------------------

type youtubeChatMessageDTO struct {
	ID           string `json:"id"`
	Text         string `json:"text"`
	DisplayName  string `json:"displayName"`
	ChannelID    string `json:"channelId"`
	ProfileImage string `json:"profileImage"`
	Mod          bool   `json:"mod"`
	Subscriber   bool   `json:"subscriber"`
	Broadcaster  bool   `json:"broadcaster"`
	Timestamp    int64  `json:"timestamp"`
}

// youtubeStreamErrorDTO is the payload of a "streamError" SSE event.
// Fatal means the server has stopped retrying and is closing the
// response, so the browser must not reconnect.
type youtubeStreamErrorDTO struct {
	Message string `json:"message"`
	Fatal   bool   `json:"fatal"`
}

// handleYouTubeChatStream relays a live chat to the browser as
// server-sent events. It doesn't talk to YouTube itself: it subscribes
// to the shared youtubeChatHub for the chat (youtube_chat_hub.go), which
// holds one liveChatMessages.streamList call open for every tab watching
// that chat, replays its recent backlog to this tab, then forwards new
// events as they arrive.
func (s *Server) handleYouTubeChatStream(w http.ResponseWriter, r *http.Request) {
	if s.youtubeSessionFromRequest(r) == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	// youtubeSessionFromRequest succeeding means the cookie is present.
	cookie, _ := r.Cookie(youtubeSessionCookieName)

	liveChatID := strings.TrimSpace(r.URL.Query().Get("liveChatId"))
	if liveChatID == "" {
		http.Error(w, "missing liveChatId query parameter", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	hub, sub, replay, err := s.subscribeYouTubeChat(liveChatID, cookie.Value)
	if err != nil {
		http.Error(w, "could not reach youtube", http.StatusBadGateway)
		return
	}
	defer hub.unsubscribe(sub)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	for _, ev := range replay {
		ev.writeTo(w)
	}
	flusher.Flush()

	// streamList only pushes when there's something to say, so without
	// this an idle chat could leave the connection looking dead to a
	// proxy sitting between us and the browser.
	keepalive := time.NewTicker(youtubeStreamKeepaliveInterval)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			w.Write([]byte(": keepalive\n\n"))
			flusher.Flush()
		case ev, ok := <-sub.ch:
			if !ok {
				// The hub stopped relaying to this tab. If it ended with
				// a fatal error, pass it on — named "streamError", not
				// "error", since EventSource fires its own native "error"
				// on connection trouble. Otherwise (this tab fell behind)
				// just end the response: EventSource reconnects and
				// catches up from the backlog.
				if final := hub.finalEvent(); final != nil {
					final.writeTo(w)
					flusher.Flush()
				}
				return
			}
			ev.writeTo(w)
			flusher.Flush()
		}
	}
}
