package server

import (
	"encoding/json"
	"fmt"
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
		redirectWithError(w, r, returnTo, msg)
		return
	}
	if msg := q.Get("error"); msg != "" {
		redirectWithError(w, r, returnTo, msg)
		return
	}

	code := q.Get("code")
	if code == "" {
		redirectWithError(w, r, returnTo, "youtube did not return an authorization code")
		return
	}

	ctx := r.Context()
	tok, err := exchangeYouTubeCode(ctx, s.cfg, code)
	if err != nil {
		redirectWithError(w, r, returnTo, "could not complete youtube login")
		return
	}

	channel, err := fetchOwnYouTubeChannel(ctx, tok.AccessToken)
	if err != nil {
		redirectWithError(w, r, returnTo, "could not load youtube channel")
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
		redirectWithError(w, r, returnTo, "could not start session")
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
	LiveChatID string `json:"liveChatId"`
	ChannelID  string `json:"channelId"`
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
	json.NewEncoder(w).Encode(youtubeLiveChatRefDTO{LiveChatID: liveChatID, ChannelID: sess.ChannelID})
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

	liveChatID, channelID, err := resolveVideoLiveChat(r.Context(), sess.AccessToken, videoID)
	if err != nil {
		writeYouTubeError(w, err, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(youtubeLiveChatRefDTO{LiveChatID: liveChatID, ChannelID: channelID})
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

const (
	youtubeMinPollInterval = 2 * time.Second
	youtubeMaxPollInterval = 15 * time.Second
)

// handleYouTubeChatStream polls liveChatMessages.list and relays new
// text messages to the browser as server-sent events. YouTube's API is
// pull-based (no websocket/webhook for chat), so — unlike Twitch's
// always-open IRC connection or Kick's webhook fan-out — this handler
// owns a polling loop for as long as the browser tab stays open. It
// re-resolves the session on every cycle (cheap: only refreshes the
// token over the network when actually close to expiring) since a
// viewer could easily keep a tab open longer than a ~1 hour access
// token's lifetime.
func (s *Server) handleYouTubeChatStream(w http.ResponseWriter, r *http.Request) {
	if s.youtubeSessionFromRequest(r) == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

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

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()
	pageToken := ""
	interval := 5 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}

		sess := s.youtubeSessionFromRequest(r)
		if sess == nil {
			// Refresh failed (e.g. access revoked) or the cookie is
			// gone; nothing more we can do for this stream.
			return
		}

		params := url.Values{"part": {"snippet,authorDetails"}, "liveChatId": {liveChatID}}
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}

		var out struct {
			Items []struct {
				ID      string `json:"id"`
				Snippet struct {
					Type              string `json:"type"`
					DisplayMessage    string `json:"displayMessage"`
					HasDisplayContent bool   `json:"hasDisplayContent"`
					PublishedAt       string `json:"publishedAt"`
				} `json:"snippet"`
				AuthorDetails struct {
					ChannelID       string `json:"channelId"`
					DisplayName     string `json:"displayName"`
					ProfileImageURL string `json:"profileImageUrl"`
					IsChatOwner     bool   `json:"isChatOwner"`
					IsChatModerator bool   `json:"isChatModerator"`
					IsChatSponsor   bool   `json:"isChatSponsor"`
				} `json:"authorDetails"`
			} `json:"items"`
			NextPageToken         string `json:"nextPageToken"`
			PollingIntervalMillis int    `json:"pollingIntervalMillis"`
		}

		err := youtubeAPIRequest(ctx, http.MethodGet, youtubeAPIBase+"/liveChat/messages?"+params.Encode(), sess.AccessToken, nil, &out)
		if err != nil {
			if apiErr, ok := err.(*youtubeAPIError); ok && (apiErr.Status == 401 || apiErr.Status == 403 || apiErr.Status == 404) {
				// Chat ended, access revoked, or the ID is no longer
				// valid — no point retrying indefinitely.
				// Named "streamError", not "error" — EventSource fires
				// its own native "error" event on connection trouble, and
				// listening for both under the same name would make a
				// business-logic error (e.g. chat ended) look like a
				// dropped connection to the browser.
				fmt.Fprintf(w, "event: streamError\ndata: %s\n\n", mustJSON(map[string]string{"message": apiErr.Message}))
				flusher.Flush()
				return
			}
			// Transient error (network blip, rate limit): back off and
			// try again rather than tearing down the viewer's stream.
			continue
		}

		pageToken = out.NextPageToken
		if out.PollingIntervalMillis > 0 {
			interval = time.Duration(out.PollingIntervalMillis) * time.Millisecond
			if interval < youtubeMinPollInterval {
				interval = youtubeMinPollInterval
			} else if interval > youtubeMaxPollInterval {
				interval = youtubeMaxPollInterval
			}
		}

		wrote := false
		for _, item := range out.Items {
			if item.Snippet.Type != "textMessageEvent" || !item.Snippet.HasDisplayContent {
				continue
			}
			ts := time.Now().UnixMilli()
			if parsed, err := time.Parse(time.RFC3339, item.Snippet.PublishedAt); err == nil {
				ts = parsed.UnixMilli()
			}
			writeSSE(w, "message", youtubeChatMessageDTO{
				ID:           item.ID,
				Text:         item.Snippet.DisplayMessage,
				DisplayName:  item.AuthorDetails.DisplayName,
				ChannelID:    item.AuthorDetails.ChannelID,
				ProfileImage: item.AuthorDetails.ProfileImageURL,
				Mod:          item.AuthorDetails.IsChatModerator,
				Subscriber:   item.AuthorDetails.IsChatSponsor,
				Broadcaster:  item.AuthorDetails.IsChatOwner,
				Timestamp:    ts,
			})
			wrote = true
		}
		if !wrote {
			fmt.Fprint(w, ": keepalive\n\n")
		}
		flusher.Flush()
	}
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return data
}
