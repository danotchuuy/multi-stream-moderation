package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	sessionCookieName = "msm_session"
	sessionTTL        = 30 * 24 * time.Hour
	stateTTL          = 10 * time.Minute

	// tokenRefreshBuffer is how far ahead of Twitch access-token expiry
	// we proactively refresh, so a request doesn't race an expiring
	// token.
	tokenRefreshBuffer = 5 * time.Minute

	// sessionKeyFileName is the file, alongside the session store, that
	// holds its auto-generated at-rest encryption key when one isn't
	// supplied via Config.SessionEncryptionKey.
	sessionKeyFileName = "session.key"
)

// twitchSession holds the authenticated Twitch identity and tokens for
// one browser session.
//
// This is the sensitive half of the BFF (Backend-For-Frontend) split:
// the browser only ever holds an opaque, unguessable session cookie (see
// setSessionCookie) — it never sees AccessToken or RefreshToken, and no
// handler ever puts them in a JSON response. Twitch API calls that need
// them happen entirely server-side. Sessions are additionally persisted
// to disk, encrypted (see authStore), so restarting the server doesn't
// force the user to reauthorize.
type twitchSession struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	UserID       string    `json:"userId"`
	Login        string    `json:"login"`
	DisplayName  string    `json:"displayName"`
	ProfileImage string    `json:"profileImage"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// String redacts the token fields so an accidental %v/%+v of a session
// (e.g. in a stray log line) can't leak credentials.
func (s twitchSession) String() string {
	return fmt.Sprintf("twitchSession{Login:%s UserID:%s ExpiresAt:%s}", s.Login, s.UserID, s.ExpiresAt)
}

type stateEntry struct {
	returnTo  string
	expiresAt time.Time
}

// authStore keeps in-memory OAuth state (CSRF tokens awaiting a Twitch
// callback) and established sessions. Sessions are additionally mirrored
// to disk, AES-256-GCM encrypted, at path (if set) so they survive a
// server restart; OAuth login attempts (states) are short-lived and only
// ever kept in memory.
type authStore struct {
	mu       sync.Mutex
	states   map[string]stateEntry
	sessions map[string]*twitchSession

	path   string
	key    []byte // nil disables persistence, e.g. if key setup failed
	saveMu sync.Mutex
}

// newAuthStore builds a store, loading and decrypting any existing
// session file at path. base64Key, if set, is used as the at-rest
// encryption key (32 bytes, base64-encoded); otherwise a key is
// generated on first use and cached next to path. If key setup or
// loading fails, the store still starts up — it just won't persist
// sessions across restarts, which is logged rather than fatal.
func newAuthStore(path, base64Key string) *authStore {
	s := &authStore{
		states:   make(map[string]stateEntry),
		sessions: make(map[string]*twitchSession),
		path:     path,
	}
	if path == "" {
		return s
	}

	key, err := resolveEncryptionKey(path, base64Key)
	if err != nil {
		log.Printf("twitch: could not set up session encryption, sessions will not persist across restarts: %v", err)
		return s
	}
	s.key = key

	sessions, err := loadSessions(path, key)
	if err != nil {
		log.Printf("twitch: could not load saved sessions from %s: %v", path, err)
	} else if sessions != nil {
		s.sessions = sessions
	}
	return s
}

// resolveEncryptionKey returns the 32-byte AES-256 key used to encrypt
// the session store: base64Key if explicitly configured, otherwise a
// key cached in a file next to the session store (generated on first
// run). Keeping the key in a separate file from the sessions it
// protects means a leak of just one of the two files (e.g. it ending up
// in a broader backup that the other doesn't) isn't enough on its own.
func resolveEncryptionKey(sessionPath, base64Key string) ([]byte, error) {
	if base64Key != "" {
		key, err := base64.StdEncoding.DecodeString(base64Key)
		if err != nil {
			return nil, fmt.Errorf("invalid session encryption key: %w", err)
		}
		if len(key) != encryptionKeySize {
			return nil, fmt.Errorf("session encryption key must decode to %d bytes, got %d", encryptionKeySize, len(key))
		}
		return key, nil
	}

	keyPath := filepath.Join(filepath.Dir(sessionPath), sessionKeyFileName)
	data, err := os.ReadFile(keyPath)
	if err == nil {
		if len(data) != encryptionKeySize {
			return nil, fmt.Errorf("%s does not contain a valid %d-byte key", keyPath, encryptionKeySize)
		}
		return data, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	key := make([]byte, encryptionKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// putState records a one-time CSRF token for an in-flight OAuth login,
// remembering where to send the browser back to once it completes.
func (s *authStore) putState(returnTo string) (string, error) {
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

// takeState validates and consumes a one-time OAuth state token.
func (s *authStore) takeState(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.states[token]
	delete(s.states, token)
	if !ok || time.Now().After(entry.expiresAt) {
		return "", false
	}
	return entry.returnTo, true
}

func (s *authStore) putSession(sess *twitchSession) (string, error) {
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

func (s *authStore) session(token string) *twitchSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[token]
}

// updateSession replaces the session stored under token, e.g. after
// refreshing its access token.
func (s *authStore) updateSession(token string, sess *twitchSession) {
	s.mu.Lock()
	s.sessions[token] = sess
	s.mu.Unlock()

	s.persist()
}

func (s *authStore) deleteSession(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()

	s.persist()
}

// persist writes the current sessions to disk, logging (rather than
// failing the request) if it can't. Losing the on-disk copy just means a
// later restart will require reauthorizing, not that the running server
// misbehaves.
func (s *authStore) persist() {
	if s.path == "" || s.key == nil {
		return
	}
	s.mu.Lock()
	snapshot := make(map[string]*twitchSession, len(s.sessions))
	for k, v := range s.sessions {
		snapshot[k] = v
	}
	s.mu.Unlock()

	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if err := saveSessions(s.path, s.key, snapshot); err != nil {
		log.Printf("twitch: could not persist sessions to %s: %v", s.path, err)
	}
}

type persistedSessions struct {
	Sessions map[string]*twitchSession `json:"sessions"`
}

// loadSessions reads and decrypts the session store at path. A missing
// file is not an error (first run).
func loadSessions(path string, key []byte) (map[string]*twitchSession, error) {
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
	var p persistedSessions
	if err := json.Unmarshal(plaintext, &p); err != nil {
		return nil, err
	}
	return p.Sessions, nil
}

// saveSessions encrypts sessions and writes them to path, replacing any
// existing file atomically (write-then-rename). File permissions are
// restrictive as defense in depth, but encryption — not file mode — is
// what actually protects the tokens if the file is copied out (backups,
// sync tools, etc).
func saveSessions(path string, key []byte, sessions map[string]*twitchSession) error {
	plaintext, err := json.Marshal(persistedSessions{Sessions: sessions})
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

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// sessionFromRequest resolves the caller's session from its cookie,
// transparently refreshing the Twitch access token first if it's
// missing, expired, or close to expiring. A refresh failure (e.g. the
// user revoked access) drops the session so the UI falls back to
// prompting for login.
func (s *Server) sessionFromRequest(r *http.Request) *twitchSession {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil
	}
	sess := s.auth.session(c.Value)
	if sess == nil {
		return nil
	}
	if sess.ExpiresAt.IsZero() || time.Until(sess.ExpiresAt) > tokenRefreshBuffer {
		return sess
	}

	refreshed, err := s.refreshSession(r.Context(), c.Value, sess)
	if err != nil {
		log.Printf("twitch: could not refresh session for %s: %v", sess.Login, err)
		s.auth.deleteSession(c.Value)
		return nil
	}
	return refreshed
}

func (s *Server) refreshSession(ctx context.Context, token string, sess *twitchSession) (*twitchSession, error) {
	if sess.RefreshToken == "" {
		return nil, errors.New("session has no refresh token")
	}
	tok, err := refreshTwitchToken(ctx, s.cfg, sess.RefreshToken)
	if err != nil {
		return nil, err
	}

	updated := *sess
	updated.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		updated.RefreshToken = tok.RefreshToken
	}
	updated.ExpiresAt = tokenExpiry(tok)

	s.auth.updateSession(token, &updated)
	return &updated, nil
}

// setSessionCookie hands the browser only an opaque session token — the
// actual Twitch tokens stay server-side. HttpOnly keeps it out of reach
// of JS (XSS can't exfiltrate it), SameSite=Lax keeps it off cross-site
// requests (CSRF), and Secure (enabled whenever the server is configured
// for HTTPS) keeps it off plaintext connections.
func (s *Server) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(sessionTTL),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
