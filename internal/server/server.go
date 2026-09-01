// Package server wires up the HTTP API and (in production) serves the
// built UI as static files alongside it.
package server

import (
	"net/http"
	"os"
	"sync"
)

// Config controls how the server is constructed.
type Config struct {
	// Port the HTTP server listens on.
	Port string
	// UIDistDir is the path to the built UI assets (web/dist after
	// `npm run build`). If the directory doesn't exist, static file
	// serving is skipped so the Go API can still run standalone behind
	// the Vite dev server during development.
	UIDistDir string

	// TwitchClientID and TwitchClientSecret identify this app to Twitch's
	// OAuth server (id.twitch.tv). Create an app at
	// https://dev.twitch.tv/console/apps to obtain them.
	TwitchClientID     string
	TwitchClientSecret string
	// TwitchRedirectURL is the OAuth callback URL registered for the app
	// above; it must exactly match one of the app's "OAuth Redirect URLs".
	TwitchRedirectURL string
	// SessionStoreFile is where authenticated sessions (including OAuth
	// access/refresh tokens) are persisted, encrypted, so restarting the
	// server doesn't force every user to reauthorize. Sessions are kept
	// in memory only if left empty.
	SessionStoreFile string
	// SessionEncryptionKey is a base64-encoded 32-byte AES-256 key used
	// to encrypt the session store at rest. If empty, a key is
	// generated on first run and cached next to SessionStoreFile.
	SessionEncryptionKey string
	// CookieSecure marks the session cookie Secure (HTTPS-only). Enable
	// it whenever the server is reachable over HTTPS; leave it false for
	// plain-HTTP local development, where a Secure cookie would simply
	// never be sent.
	CookieSecure bool
	// AllowedOrigin, if set, is the single origin (e.g.
	// "https://mod.example.com") allowed to make cross-origin,
	// credentialed requests to the API. Leave empty unless the UI is
	// genuinely served from a different origin than this server — the
	// documented dev setup (Vite's proxy) and the production setup
	// (this server serving the built UI) both talk to the API
	// same-origin and need no CORS grant at all.
	AllowedOrigin string

	// KickClientID and KickClientSecret identify this app to Kick's
	// OAuth server (id.kick.com). Create an app at
	// https://kick.com/settings/developer to obtain them.
	KickClientID     string
	KickClientSecret string
	// KickRedirectURL is the OAuth callback URL registered for the app
	// above; it must exactly match the app's configured redirect URL.
	KickRedirectURL string
	// KickSessionStoreFile is where authenticated Kick sessions are
	// persisted, encrypted — the Kick equivalent of SessionStoreFile.
	// Kept as a separate file (and separate cookie) from the Twitch
	// session store so the two integrations can't interfere with each
	// other, but reuses the same at-rest encryption key when both files
	// live in the same directory (see auth.go's resolveEncryptionKey).
	KickSessionStoreFile string
}

// Server bundles the HTTP server and its dependencies.
type Server struct {
	cfg  Config
	mux  *http.ServeMux
	auth *authStore

	// channelIDs caches the login -> broadcaster ID lookups needed by
	// the moderation endpoints, which take numeric IDs rather than
	// channel names.
	channelIDsMu sync.Mutex
	channelIDs   map[string]string

	kickAuth      *kickAuthStore
	kickHub       *kickHub
	kickPublicKey *kickPublicKeyCache

	// kickChannelIDs caches the slug -> broadcaster_user_id lookups
	// needed by Kick's moderation and event-subscription endpoints.
	kickChannelIDsMu sync.Mutex
	kickChannelIDs   map[string]int64
}

// New builds a Server ready to serve.
func New(cfg Config) *Server {
	s := &Server{
		cfg:        cfg,
		mux:        http.NewServeMux(),
		auth:       newAuthStore(cfg.SessionStoreFile, cfg.SessionEncryptionKey),
		channelIDs: make(map[string]string),

		kickAuth:       newKickAuthStore(cfg.KickSessionStoreFile, cfg.SessionEncryptionKey),
		kickHub:        newKickHub(),
		kickPublicKey:  &kickPublicKeyCache{},
		kickChannelIDs: make(map[string]int64),
	}
	s.routes()
	return s
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	return http.ListenAndServe(":"+s.cfg.Port, s.withCORS(s.mux))
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/health", s.handleHealth)

	s.mux.HandleFunc("GET /api/auth/twitch/login", s.handleTwitchLogin)
	s.mux.HandleFunc("GET /api/auth/twitch/callback", s.handleTwitchCallback)
	s.mux.HandleFunc("GET /api/auth/session", s.handleAuthSession)
	s.mux.HandleFunc("POST /api/auth/logout", s.handleAuthLogout)

	s.mux.HandleFunc("GET /api/twitch/moderated-channels", s.handleModeratedChannels)
	s.mux.HandleFunc("GET /api/chat/stream", s.handleChatStream)

	s.mux.HandleFunc("DELETE /api/chat/messages", s.handleDeleteChatMessage)
	s.mux.HandleFunc("POST /api/chat/bans", s.handleBanUser)
	s.mux.HandleFunc("POST /api/chat/timeouts", s.handleTimeoutUser)

	s.mux.HandleFunc("GET /api/auth/kick/login", s.handleKickLogin)
	s.mux.HandleFunc("GET /api/auth/kick/callback", s.handleKickCallback)
	s.mux.HandleFunc("GET /api/auth/kick/session", s.handleKickAuthSession)
	s.mux.HandleFunc("POST /api/auth/kick/logout", s.handleKickAuthLogout)

	s.mux.HandleFunc("GET /api/kick/channel", s.handleKickOwnChannel)
	s.mux.HandleFunc("GET /api/kick/chat/stream", s.handleKickChatStream)
	s.mux.HandleFunc("DELETE /api/kick/chat/messages", s.handleDeleteKickChatMessage)
	s.mux.HandleFunc("POST /api/kick/bans", s.handleBanKickUser)
	s.mux.HandleFunc("POST /api/kick/timeouts", s.handleTimeoutKickUser)
	// Called by Kick's servers, not the browser — no session cookie, no
	// CORS involved; authenticated instead via RSA signature (see
	// kick_webhooks.go).
	s.mux.HandleFunc("POST /api/kick/webhook", s.handleKickWebhook)

	if info, err := os.Stat(s.cfg.UIDistDir); err == nil && info.IsDir() {
		s.mux.Handle("/", http.FileServer(http.Dir(s.cfg.UIDistDir)))
	}
}

// withCORS grants cross-origin access to s.cfg.AllowedOrigin only, if
// configured. No CORS headers are sent at all otherwise (the safe
// default): the documented dev setup (Vite's proxy) and the production
// setup (this server serving the built UI) both reach the API
// same-origin, where CORS doesn't even apply, so a session cookie — the
// one credential a cross-origin request could try to ride along — is
// never exposed to a third-party origin by default. A wildcard origin is
// deliberately not offered: combined with a credentialed request it
// would let any site read authenticated responses.
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AllowedOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", s.cfg.AllowedOrigin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Vary", "Origin")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
