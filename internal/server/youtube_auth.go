package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const youtubeSessionCookieName = "msm_youtube_session"

// youtubeSession holds the authenticated Google identity and tokens for
// one browser session. Same BFF split as twitchSession/kickSession: the
// browser only ever holds an opaque session cookie, never these tokens.
type youtubeSession struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	ChannelID    string    `json:"channelId"`
	ChannelTitle string    `json:"channelTitle"`
	ProfileImage string    `json:"profileImage"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// String redacts the token fields, mirroring twitchSession.String.
func (s youtubeSession) String() string {
	return "youtubeSession{ChannelTitle:" + s.ChannelTitle + "}"
}

// youtubeAuthStore mirrors authStore (see auth.go) for YouTube sessions.
// Google's OAuth flow needs no PKCE step (unlike Kick's), so — unlike
// kickAuthStore — this reuses auth.go's plain stateEntry (return_to
// only) rather than needing its own state type.
type youtubeAuthStore struct {
	mu       sync.Mutex
	states   map[string]stateEntry
	sessions map[string]*youtubeSession

	path   string
	key    []byte
	saveMu sync.Mutex
}

func newYouTubeAuthStore(path, base64Key string) *youtubeAuthStore {
	s := &youtubeAuthStore{
		states:   make(map[string]stateEntry),
		sessions: make(map[string]*youtubeSession),
		path:     path,
	}
	if path == "" {
		return s
	}

	key, err := resolveEncryptionKey(path, base64Key)
	if err != nil {
		log.Printf("youtube: could not set up session encryption, sessions will not persist across restarts: %v", err)
		return s
	}
	s.key = key

	sessions, err := loadYouTubeSessions(path, key)
	if err != nil {
		log.Printf("youtube: could not load saved sessions from %s: %v", path, err)
	} else if sessions != nil {
		s.sessions = sessions
	}
	return s
}

func (s *youtubeAuthStore) putState(returnTo string) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.states {
		if time.Now().After(v.expiresAt) {
			delete(s.states, k)
		}
	}
	s.states[token] = stateEntry{returnTo: returnTo, expiresAt: time.Now().Add(stateTTL)}
	return token, nil
}

func (s *youtubeAuthStore) takeState(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.states[token]
	delete(s.states, token)
	if !ok || time.Now().After(entry.expiresAt) {
		return "", false
	}
	return entry.returnTo, true
}

func (s *youtubeAuthStore) putSession(sess *youtubeSession) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.sessions[token] = sess
	s.mu.Unlock()

	s.persist()
	return token, nil
}

func (s *youtubeAuthStore) session(token string) *youtubeSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[token]
}

func (s *youtubeAuthStore) updateSession(token string, sess *youtubeSession) {
	s.mu.Lock()
	s.sessions[token] = sess
	s.mu.Unlock()

	s.persist()
}

func (s *youtubeAuthStore) deleteSession(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()

	s.persist()
}

func (s *youtubeAuthStore) persist() {
	if s.path == "" || s.key == nil {
		return
	}
	s.mu.Lock()
	snapshot := make(map[string]*youtubeSession, len(s.sessions))
	for k, v := range s.sessions {
		snapshot[k] = v
	}
	s.mu.Unlock()

	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if err := saveYouTubeSessions(s.path, s.key, snapshot); err != nil {
		log.Printf("youtube: could not persist sessions to %s: %v", s.path, err)
	}
}

type persistedYouTubeSessions struct {
	Sessions map[string]*youtubeSession `json:"sessions"`
}

func loadYouTubeSessions(path string, key []byte) (map[string]*youtubeSession, error) {
	ciphertext, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	plaintext, err := decrypt(key, ciphertext)
	if err != nil {
		return nil, err
	}
	var p persistedYouTubeSessions
	if err := json.Unmarshal(plaintext, &p); err != nil {
		return nil, err
	}
	return p.Sessions, nil
}

func saveYouTubeSessions(path string, key []byte, sessions map[string]*youtubeSession) error {
	plaintext, err := json.Marshal(persistedYouTubeSessions{Sessions: sessions})
	if err != nil {
		return err
	}
	ciphertext, err := encrypt(key, plaintext)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, ciphertext, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// youtubeSessionFromRequest mirrors Server.sessionFromRequest for the
// YouTube cookie/session pair, refreshing the access token if it's
// expired or close to it.
func (s *Server) youtubeSessionFromRequest(r *http.Request) *youtubeSession {
	c, err := r.Cookie(youtubeSessionCookieName)
	if err != nil {
		return nil
	}
	return s.youtubeSessionByToken(r.Context(), c.Value)
}

// youtubeSessionByToken looks up a session by its cookie value,
// refreshing the access token if it's expired or close to it. Used
// directly by youtubeChatHub, which outlives the request that
// subscribed it.
func (s *Server) youtubeSessionByToken(ctx context.Context, token string) *youtubeSession {
	sess := s.youtubeAuth.session(token)
	if sess == nil {
		return nil
	}
	if sess.ExpiresAt.IsZero() || time.Until(sess.ExpiresAt) > tokenRefreshBuffer {
		return sess
	}

	refreshed, err := s.refreshYouTubeSession(ctx, token, sess)
	if err != nil {
		log.Printf("youtube: could not refresh session for %s: %v", sess.ChannelTitle, err)
		s.youtubeAuth.deleteSession(token)
		return nil
	}
	return refreshed
}

func (s *Server) refreshYouTubeSession(ctx context.Context, token string, sess *youtubeSession) (*youtubeSession, error) {
	if sess.RefreshToken == "" {
		return nil, errors.New("session has no refresh token")
	}
	tok, err := refreshYouTubeToken(ctx, s.cfg, sess.RefreshToken)
	if err != nil {
		return nil, err
	}

	updated := *sess
	updated.AccessToken = tok.AccessToken
	// Google's refresh response normally omits refresh_token — the
	// original stays valid — so only overwrite it if a new one actually
	// came back.
	if tok.RefreshToken != "" {
		updated.RefreshToken = tok.RefreshToken
	}
	updated.ExpiresAt = youtubeTokenExpiry(tok)

	s.youtubeAuth.updateSession(token, &updated)
	return &updated, nil
}

func (s *Server) setYouTubeSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     youtubeSessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(sessionTTL),
	})
}

func (s *Server) clearYouTubeSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     youtubeSessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
