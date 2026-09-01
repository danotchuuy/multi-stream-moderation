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

const kickSessionCookieName = "msm_kick_session"

// kickSession holds the authenticated Kick identity and tokens for one
// browser session. Same BFF split as twitchSession (see auth.go): the
// browser only ever holds an opaque session cookie, never these tokens.
type kickSession struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	UserID       int64     `json:"userId"`
	Username     string    `json:"username"`
	ProfileImage string    `json:"profileImage"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// String redacts the token fields, mirroring twitchSession.String.
func (s kickSession) String() string {
	return "kickSession{Username:" + s.Username + "}"
}

// kickStateEntry additionally carries the PKCE code_verifier generated
// at login time, which must be replayed at token-exchange time — Kick
// requires PKCE, unlike Twitch's plain authorization code flow.
type kickStateEntry struct {
	returnTo     string
	codeVerifier string
	expiresAt    time.Time
}

// kickAuthStore mirrors authStore (see auth.go) for Kick sessions. It's
// a separate store — rather than generalizing authStore to be
// multi-provider — so the well-tested Twitch persistence/session logic
// doesn't need touching for Kick's extra PKCE step.
type kickAuthStore struct {
	mu       sync.Mutex
	states   map[string]kickStateEntry
	sessions map[string]*kickSession

	path   string
	key    []byte
	saveMu sync.Mutex
}

func newKickAuthStore(path, base64Key string) *kickAuthStore {
	s := &kickAuthStore{
		states:   make(map[string]kickStateEntry),
		sessions: make(map[string]*kickSession),
		path:     path,
	}
	if path == "" {
		return s
	}

	key, err := resolveEncryptionKey(path, base64Key)
	if err != nil {
		log.Printf("kick: could not set up session encryption, sessions will not persist across restarts: %v", err)
		return s
	}
	s.key = key

	sessions, err := loadKickSessions(path, key)
	if err != nil {
		log.Printf("kick: could not load saved sessions from %s: %v", path, err)
	} else if sessions != nil {
		s.sessions = sessions
	}
	return s
}

func (s *kickAuthStore) putState(returnTo, codeVerifier string) (string, error) {
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
	s.states[token] = kickStateEntry{
		returnTo:     returnTo,
		codeVerifier: codeVerifier,
		expiresAt:    time.Now().Add(stateTTL),
	}
	return token, nil
}

func (s *kickAuthStore) takeState(token string) (kickStateEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.states[token]
	delete(s.states, token)
	if !ok || time.Now().After(entry.expiresAt) {
		return kickStateEntry{}, false
	}
	return entry, true
}

func (s *kickAuthStore) putSession(sess *kickSession) (string, error) {
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

func (s *kickAuthStore) session(token string) *kickSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[token]
}

func (s *kickAuthStore) updateSession(token string, sess *kickSession) {
	s.mu.Lock()
	s.sessions[token] = sess
	s.mu.Unlock()

	s.persist()
}

func (s *kickAuthStore) deleteSession(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()

	s.persist()
}

func (s *kickAuthStore) persist() {
	if s.path == "" || s.key == nil {
		return
	}
	s.mu.Lock()
	snapshot := make(map[string]*kickSession, len(s.sessions))
	for k, v := range s.sessions {
		snapshot[k] = v
	}
	s.mu.Unlock()

	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if err := saveKickSessions(s.path, s.key, snapshot); err != nil {
		log.Printf("kick: could not persist sessions to %s: %v", s.path, err)
	}
}

type persistedKickSessions struct {
	Sessions map[string]*kickSession `json:"sessions"`
}

func loadKickSessions(path string, key []byte) (map[string]*kickSession, error) {
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
	var p persistedKickSessions
	if err := json.Unmarshal(plaintext, &p); err != nil {
		return nil, err
	}
	return p.Sessions, nil
}

func saveKickSessions(path string, key []byte, sessions map[string]*kickSession) error {
	plaintext, err := json.Marshal(persistedKickSessions{Sessions: sessions})
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

// kickSessionFromRequest mirrors Server.sessionFromRequest for the Kick
// cookie/session pair, refreshing the access token if it's expired or
// close to it.
func (s *Server) kickSessionFromRequest(r *http.Request) *kickSession {
	c, err := r.Cookie(kickSessionCookieName)
	if err != nil {
		return nil
	}
	sess := s.kickAuth.session(c.Value)
	if sess == nil {
		return nil
	}
	if sess.ExpiresAt.IsZero() || time.Until(sess.ExpiresAt) > tokenRefreshBuffer {
		return sess
	}

	refreshed, err := s.refreshKickSession(r.Context(), c.Value, sess)
	if err != nil {
		log.Printf("kick: could not refresh session for %s: %v", sess.Username, err)
		s.kickAuth.deleteSession(c.Value)
		return nil
	}
	return refreshed
}

func (s *Server) refreshKickSession(ctx context.Context, token string, sess *kickSession) (*kickSession, error) {
	if sess.RefreshToken == "" {
		return nil, errors.New("session has no refresh token")
	}
	tok, err := refreshKickToken(ctx, s.cfg, sess.RefreshToken)
	if err != nil {
		return nil, err
	}

	updated := *sess
	updated.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		updated.RefreshToken = tok.RefreshToken
	}
	updated.ExpiresAt = kickTokenExpiry(tok)

	s.kickAuth.updateSession(token, &updated)
	return &updated, nil
}

func (s *Server) setKickSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     kickSessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(sessionTTL),
	})
}

func (s *Server) clearKickSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     kickSessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
