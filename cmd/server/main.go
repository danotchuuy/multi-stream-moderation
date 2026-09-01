package main

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/danotchuuy/multi-stream-moderation/internal/server"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	uiDir := os.Getenv("UI_DIST_DIR")
	if uiDir == "" {
		uiDir = "web/dist"
	}

	redirectURL := os.Getenv("TWITCH_REDIRECT_URL")
	if redirectURL == "" {
		redirectURL = "http://localhost:" + port + "/api/auth/twitch/callback"
	}

	kickRedirectURL := os.Getenv("KICK_REDIRECT_URL")
	if kickRedirectURL == "" {
		kickRedirectURL = "http://localhost:" + port + "/api/auth/kick/callback"
	}

	youtubeRedirectURL := os.Getenv("YOUTUBE_REDIRECT_URL")
	if youtubeRedirectURL == "" {
		youtubeRedirectURL = "http://localhost:" + port + "/api/auth/youtube/callback"
	}

	sessionStoreFile := os.Getenv("SESSION_STORE_FILE")
	kickSessionStoreFile := os.Getenv("KICK_SESSION_STORE_FILE")
	youtubeSessionStoreFile := os.Getenv("YOUTUBE_SESSION_STORE_FILE")
	if sessionStoreFile == "" || kickSessionStoreFile == "" || youtubeSessionStoreFile == "" {
		if configDir, err := os.UserConfigDir(); err == nil {
			// .enc, not .json: the files are AES-256-GCM encrypted, not
			// plain text — see internal/server/auth.go. All three live in
			// the same directory so they share one auto-generated
			// encryption key (session.key) rather than needing three.
			if sessionStoreFile == "" {
				sessionStoreFile = filepath.Join(configDir, "multi-stream-moderation", "sessions.enc")
			}
			if kickSessionStoreFile == "" {
				kickSessionStoreFile = filepath.Join(configDir, "multi-stream-moderation", "kick-sessions.enc")
			}
			if youtubeSessionStoreFile == "" {
				youtubeSessionStoreFile = filepath.Join(configDir, "multi-stream-moderation", "youtube-sessions.enc")
			}
		}
	}

	cookieSecure := strings.HasPrefix(redirectURL, "https://")
	if v := os.Getenv("COOKIE_SECURE"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cookieSecure = parsed
		} else {
			log.Printf("Invalid COOKIE_SECURE value %q, ignoring.", v)
		}
	}

	config := server.Config{
		Port:                    port,
		UIDistDir:               uiDir,
		TwitchClientID:          os.Getenv("TWITCH_CLIENT_ID"),
		TwitchClientSecret:      os.Getenv("TWITCH_CLIENT_SECRET"),
		TwitchRedirectURL:       redirectURL,
		SessionStoreFile:        sessionStoreFile,
		SessionEncryptionKey:    os.Getenv("SESSION_ENCRYPTION_KEY"),
		CookieSecure:            cookieSecure,
		AllowedOrigin:           os.Getenv("ALLOWED_ORIGIN"),
		KickClientID:            os.Getenv("KICK_CLIENT_ID"),
		KickClientSecret:        os.Getenv("KICK_CLIENT_SECRET"),
		KickRedirectURL:         kickRedirectURL,
		KickSessionStoreFile:    kickSessionStoreFile,
		YouTubeClientID:         os.Getenv("YOUTUBE_CLIENT_ID"),
		YouTubeClientSecret:     os.Getenv("YOUTUBE_CLIENT_SECRET"),
		YouTubeRedirectURL:      youtubeRedirectURL,
		YouTubeSessionStoreFile: youtubeSessionStoreFile,
	}
	if config.TwitchClientID == "" {
		log.Printf("Missing twitch client id.")
	}
	if config.TwitchClientSecret == "" {
		log.Printf("Missing twitch client secret.")
	}
	if config.KickClientID == "" {
		log.Printf("Missing kick client id.")
	}
	if config.KickClientSecret == "" {
		log.Printf("Missing kick client secret.")
	}
	if config.YouTubeClientID == "" {
		log.Printf("Missing youtube client id.")
	}
	if config.YouTubeClientSecret == "" {
		log.Printf("Missing youtube client secret.")
	}
	srv := server.New(config)
	log.Printf("listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
