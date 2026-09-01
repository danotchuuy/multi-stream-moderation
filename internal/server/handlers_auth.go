package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

func (s *Server) handleTwitchLogin(w http.ResponseWriter, r *http.Request) {
	if s.cfg.TwitchClientID == "" || s.cfg.TwitchClientSecret == "" {
		http.Error(w, "twitch OAuth is not configured on the server", http.StatusServiceUnavailable)
		return
	}

	returnTo := r.URL.Query().Get("return_to")
	if returnTo == "" || !isSafeReturnTo(returnTo) {
		returnTo = "/"
	}

	state, err := s.auth.putState(returnTo)
	if err != nil {
		http.Error(w, "failed to start twitch login", http.StatusInternalServerError)
		return
	}

	params := url.Values{
		"client_id":     {s.cfg.TwitchClientID},
		"redirect_uri":  {s.cfg.TwitchRedirectURL},
		"response_type": {"code"},
		"scope":         {twitchOAuthScopes},
		"state":         {state},
	}
	http.Redirect(w, r, twitchAuthorizeURL+"?"+params.Encode(), http.StatusFound)
}

func (s *Server) handleTwitchCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	returnTo, ok := s.auth.takeState(q.Get("state"))
	if !ok {
		http.Error(w, "invalid or expired login attempt, please try again", http.StatusBadRequest)
		return
	}

	if msg := q.Get("error_description"); msg != "" {
		redirectWithError(w, r, returnTo, msg)
		return
	}

	code := q.Get("code")
	if code == "" {
		redirectWithError(w, r, returnTo, "twitch did not return an authorization code")
		return
	}

	ctx := r.Context()
	tok, err := exchangeTwitchCode(ctx, s.cfg, code)
	if err != nil {
		redirectWithError(w, r, returnTo, "could not complete twitch login")
		return
	}

	user, err := fetchTwitchUser(ctx, s.cfg.TwitchClientID, tok.AccessToken)
	if err != nil {
		redirectWithError(w, r, returnTo, "could not load twitch profile")
		return
	}

	sess := &twitchSession{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		UserID:       user.ID,
		Login:        user.Login,
		DisplayName:  user.DisplayName,
		ProfileImage: user.ProfileImageURL,
		ExpiresAt:    tokenExpiry(tok),
	}
	sessionToken, err := s.auth.putSession(sess)
	if err != nil {
		redirectWithError(w, r, returnTo, "could not start session")
		return
	}

	s.setSessionCookie(w, sessionToken)
	http.Redirect(w, r, returnTo, http.StatusFound)
}

func redirectWithError(w http.ResponseWriter, r *http.Request, returnTo, message string) {
	u, err := url.Parse(returnTo)
	if err != nil {
		u = &url.URL{Path: "/"}
	}
	q := u.Query()
	q.Set("twitch_error", message)
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// isSafeReturnTo restricts redirect targets to same-site relative paths
// or localhost URLs, so the OAuth "return_to" param can't be abused as
// an open redirect.
func isSafeReturnTo(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Host == "" {
		return strings.HasPrefix(u.Path, "/")
	}
	host := u.Hostname()
	return host == "localhost" || host == "127.0.0.1"
}

type sessionUser struct {
	Login           string `json:"login"`
	DisplayName     string `json:"displayName"`
	ProfileImageURL string `json:"profileImageUrl"`
}

type sessionResponse struct {
	Authenticated bool         `json:"authenticated"`
	User          *sessionUser `json:"user,omitempty"`
}

func (s *Server) handleAuthSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	sess := s.sessionFromRequest(r)
	if sess == nil {
		json.NewEncoder(w).Encode(sessionResponse{Authenticated: false})
		return
	}
	json.NewEncoder(w).Encode(sessionResponse{
		Authenticated: true,
		User: &sessionUser{
			Login:           sess.Login,
			DisplayName:     sess.DisplayName,
			ProfileImageURL: sess.ProfileImage,
		},
	})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		s.auth.deleteSession(c.Value)
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

type moderatedChannelDTO struct {
	BroadcasterID    string `json:"broadcasterId"`
	BroadcasterLogin string `json:"broadcasterLogin"`
	BroadcasterName  string `json:"broadcasterName"`
}

func (s *Server) handleModeratedChannels(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	channels, err := fetchModeratedChannels(r.Context(), s.cfg.TwitchClientID, sess.AccessToken, sess.UserID)
	if err != nil {
		http.Error(w, "failed to load moderated channels", http.StatusBadGateway)
		return
	}

	dtos := make([]moderatedChannelDTO, len(channels))
	for i, c := range channels {
		dtos[i] = moderatedChannelDTO{
			BroadcasterID:    c.BroadcasterID,
			BroadcasterLogin: c.BroadcasterLogin,
			BroadcasterName:  c.BroadcasterName,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Channels []moderatedChannelDTO `json:"channels"`
	}{Channels: dtos})
}
