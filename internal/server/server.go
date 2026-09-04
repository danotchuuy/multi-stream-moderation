// Package server wires up the HTTP API and (in production) serves the
// built UI as static files alongside it.
package server

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"
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

	// YouTubeClientID and YouTubeClientSecret identify this app to
	// Google's OAuth server. Create credentials at
	// https://console.cloud.google.com/apis/credentials with the
	// YouTube Data API v3 enabled.
	YouTubeClientID     string
	YouTubeClientSecret string
	// YouTubeRedirectURL is the OAuth callback URL registered for the
	// app above; it must exactly match one of its authorized redirect
	// URIs.
	YouTubeRedirectURL string
	// YouTubeSessionStoreFile is where authenticated YouTube sessions
	// are persisted, encrypted — the YouTube equivalent of
	// SessionStoreFile.
	YouTubeSessionStoreFile string
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

	// twitchBadges caches each broadcaster's badge catalog (see
	// twitch_badges.go), so a badge on an incoming chat message can be
	// resolved to Twitch's own image for it without refetching on every
	// chat connection.
	twitchBadgesMu sync.Mutex
	twitchBadges   map[string]twitchBadgeCatalog

	kickAuth      *kickAuthStore
	kickHub       *kickHub
	kickPublicKey *kickPublicKeyCache
	// kickAppToken caches the app-level access token used for webhook
	// subscription management (see kickAppTokenCache in
	// kick_webhooks.go) — never a signed-in viewer's own token, which
	// Kick would silently attribute subscriptions to instead of the
	// broadcaster actually being watched.
	kickAppToken *kickAppTokenCache

	// kickChannelIDs caches the slug -> broadcaster_user_id lookups
	// needed by Kick's moderation and event-subscription endpoints.
	kickChannelIDsMu sync.Mutex
	kickChannelIDs   map[string]int64

	// twitchChatHistory and kickChatHistory hold the last few chat
	// messages seen per channel, so a newly connected viewer gets some
	// context instead of a blank chat log. See chat_history.go.
	twitchChatHistory *chatHistoryCache[string, chatMessageDTO]
	kickChatHistory   *chatHistoryCache[int64, kickChatMessageDTO]

	youtubeAuth *youtubeAuthStore

	// youtubeChatConn is a shared gRPC connection to YouTube's live chat
	// streaming service, dialed lazily on first use by
	// youtubeChatServiceClient (see youtube_stream.go).
	youtubeChatConnOnce sync.Once
	youtubeChatConn     *grpc.ClientConn
	youtubeChatConnErr  error
}

// New builds a Server ready to serve.
func New(cfg Config) *Server {
	s := &Server{
		cfg:          cfg,
		mux:          http.NewServeMux(),
		auth:         newAuthStore(cfg.SessionStoreFile, cfg.SessionEncryptionKey),
		channelIDs:   make(map[string]string),
		twitchBadges: make(map[string]twitchBadgeCatalog),

		kickAuth:       newKickAuthStore(cfg.KickSessionStoreFile, cfg.SessionEncryptionKey),
		kickHub:        newKickHub(),
		kickPublicKey:  &kickPublicKeyCache{},
		kickAppToken:   &kickAppTokenCache{},
		kickChannelIDs: make(map[string]int64),

		twitchChatHistory: newChatHistoryCache[string, chatMessageDTO](chatHistoryLimit, func(m chatMessageDTO) string { return m.ID }),
		kickChatHistory:   newChatHistoryCache[int64, kickChatMessageDTO](chatHistoryLimit, func(m kickChatMessageDTO) string { return m.ID }),

		youtubeAuth: newYouTubeAuthStore(cfg.YouTubeSessionStoreFile, cfg.SessionEncryptionKey),
	}
	s.routes()

	// Best-effort and non-blocking: this shouldn't delay startup or stop
	// the server from coming up if Kick is slow/unreachable. clearKickWebhookSubscriptionsBestEffort
	// runs the same cleanup again on a clean shutdown (see ListenAndServe)
	// so subscriptions don't linger either way.
	go s.clearKickWebhookSubscriptionsBestEffort("startup")

	return s
}

// clearKickWebhookSubscriptionsBestEffort clears every Kick webhook
// subscription this app currently has, logging (rather than
// propagating) any failure — Kick being slow, unreachable, or rejecting
// the request is never worth blocking startup or shutdown over. No-op
// if Kick isn't configured. when is folded into the log line on
// failure ("startup" or "shutdown") so it's clear which call failed.
func (s *Server) clearKickWebhookSubscriptionsBestEffort(when string) {
	if s.cfg.KickClientID == "" || s.cfg.KickClientSecret == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := clearKickWebhookSubscriptions(ctx, s.cfg); err != nil {
		log.Printf("kick: could not clear webhook subscriptions on %s: %v", when, err)
	}
}

// ListenAndServe starts the HTTP server and blocks until it's told to
// stop — via SIGINT/SIGTERM, or the listener failing outright — clearing
// out this app's Kick webhook subscriptions on the way out either way
// they were left registered by clearKickWebhookSubscriptionsBestEffort's
// startup call, this is what removes them again on a normal shutdown,
// rather than leaving them sitting on Kick's side until the process
// happens to start back up.
func (s *Server) ListenAndServe() error {
	httpServer := &http.Server{
		Addr:    ":" + s.cfg.Port,
		Handler: s.withCORS(s.mux),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		// The listener itself failed (e.g. the port's already in use) —
		// nothing was ever serving, so there's nothing to clean up.
		return err
	case <-ctx.Done():
	}

	s.clearKickWebhookSubscriptionsBestEffort("shutdown")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/health", s.handleHealth)

	s.mux.HandleFunc("GET /api/auth/twitch/login", s.handleTwitchLogin)
	s.mux.HandleFunc("GET /api/auth/twitch/callback", s.handleTwitchCallback)
	s.mux.HandleFunc("GET /api/auth/session", s.handleAuthSession)
	s.mux.HandleFunc("POST /api/auth/logout", s.handleAuthLogout)

	s.mux.HandleFunc("GET /api/twitch/moderated-channels", s.handleModeratedChannels)
	s.mux.HandleFunc("GET /api/chat/stream", s.handleChatStream)

	s.mux.HandleFunc("GET /api/twitch/channel-info", s.handleGetTwitchChannelInfo)
	s.mux.HandleFunc("PATCH /api/twitch/channel-info", s.handleUpdateTwitchChannelInfo)
	s.mux.HandleFunc("GET /api/twitch/categories", s.handleSearchTwitchCategories)

	s.mux.HandleFunc("POST /api/chat/messages", s.handleSendChatMessage)
	s.mux.HandleFunc("DELETE /api/chat/messages", s.handleDeleteChatMessage)
	s.mux.HandleFunc("GET /api/chat/emotes", s.handleGetTwitchEmotes)
	s.mux.HandleFunc("POST /api/chat/bans", s.handleBanUser)
	s.mux.HandleFunc("POST /api/chat/timeouts", s.handleTimeoutUser)
	s.mux.HandleFunc("POST /api/chat/automod", s.handleResolveAutomodMessage)

	s.mux.HandleFunc("GET /api/auth/kick/login", s.handleKickLogin)
	s.mux.HandleFunc("GET /api/auth/kick/callback", s.handleKickCallback)
	s.mux.HandleFunc("GET /api/auth/kick/session", s.handleKickAuthSession)
	s.mux.HandleFunc("POST /api/auth/kick/logout", s.handleKickAuthLogout)

	s.mux.HandleFunc("GET /api/kick/channel", s.handleKickOwnChannel)
	s.mux.HandleFunc("GET /api/kick/channel-info", s.handleGetKickChannelInfo)
	s.mux.HandleFunc("PATCH /api/kick/channel-info", s.handleUpdateKickChannelInfo)
	s.mux.HandleFunc("GET /api/kick/categories", s.handleSearchKickCategories)
	s.mux.HandleFunc("GET /api/kick/chat/stream", s.handleKickChatStream)
	s.mux.HandleFunc("POST /api/kick/chat/messages", s.handleSendKickChatMessage)
	s.mux.HandleFunc("DELETE /api/kick/chat/messages", s.handleDeleteKickChatMessage)
	s.mux.HandleFunc("GET /api/kick/chat/emotes", s.handleGetKickEmotes)
	s.mux.HandleFunc("POST /api/kick/bans", s.handleBanKickUser)
	s.mux.HandleFunc("POST /api/kick/timeouts", s.handleTimeoutKickUser)
	// Called by Kick's servers, not the browser — no session cookie, no
	// CORS involved; authenticated instead via RSA signature (see
	// kick_webhooks.go).
	s.mux.HandleFunc("POST /api/kick/webhook", s.handleKickWebhook)

	s.mux.HandleFunc("GET /api/auth/youtube/login", s.handleYouTubeLogin)
	s.mux.HandleFunc("GET /api/auth/youtube/callback", s.handleYouTubeCallback)
	s.mux.HandleFunc("GET /api/auth/youtube/session", s.handleYouTubeAuthSession)
	s.mux.HandleFunc("POST /api/auth/youtube/logout", s.handleYouTubeAuthLogout)

	s.mux.HandleFunc("GET /api/youtube/live-chat/own", s.handleOwnYouTubeLiveChat)
	s.mux.HandleFunc("GET /api/youtube/live-chat/resolve", s.handleResolveYouTubeVideo)
	s.mux.HandleFunc("GET /api/youtube/chat/stream", s.handleYouTubeChatStream)
	s.mux.HandleFunc("POST /api/youtube/chat/messages", s.handleSendYouTubeChatMessage)
	s.mux.HandleFunc("DELETE /api/youtube/chat/messages", s.handleDeleteYouTubeChatMessage)
	s.mux.HandleFunc("POST /api/youtube/bans", s.handleBanYouTubeUser)
	s.mux.HandleFunc("POST /api/youtube/timeouts", s.handleTimeoutYouTubeUser)

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
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
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
