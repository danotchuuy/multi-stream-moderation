package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	kickAuthorizeURL = "https://id.kick.com/oauth/authorize"
	kickTokenURL     = "https://id.kick.com/oauth/token"
	kickAPIBase      = "https://api.kick.com/public/v1"

	// kickOAuthScopes lists the permissions requested from the user.
	//   - user:read, channel:read: identify the signed-in user and
	//     resolve a channel slug to its numeric ID.
	//   - moderation:chat_message:manage: delete individual messages.
	//   - moderation:ban: ban/timeout chatters.
	//   - events:subscribe: required to receive chat messages at all —
	//     Kick's public API has no endpoint to read chat, only webhooks.
	//   - chat:write: send chat messages as themselves.
	//   - channel:write: update their own stream's title and category.
	//     Kick identifies the channel purely from the token, so this only
	//     ever applies to the signed-in user's own channel.
	kickOAuthScopes = "user:read channel:read moderation:chat_message:manage moderation:ban events:subscribe chat:write channel:write"
)

// generatePKCE returns a fresh Authorization Code + PKCE pair: a random
// code_verifier and its S256 code_challenge, as Kick's OAuth server
// requires (unlike Twitch's plain authorization code flow).
// https://datatracker.ietf.org/doc/html/rfc7636
func generatePKCE() (verifier, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

type kickTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func kickTokenExpiry(tok *kickTokenResponse) time.Time {
	if tok.ExpiresIn <= 0 {
		return time.Time{}
	}
	return time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
}

func exchangeKickCode(ctx context.Context, cfg Config, code, codeVerifier string) (*kickTokenResponse, error) {
	return postKickTokenRequest(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {cfg.KickClientID},
		"client_secret": {cfg.KickClientSecret},
		"redirect_uri":  {cfg.KickRedirectURL},
		"code":          {code},
		"code_verifier": {codeVerifier},
	})
}

// refreshKickToken exchanges a refresh token for a new access token, so
// a persisted session can outlive the (typically short) access token
// without the user reauthorizing.
func refreshKickToken(ctx context.Context, cfg Config, refreshToken string) (*kickTokenResponse, error) {
	return postKickTokenRequest(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {cfg.KickClientID},
		"client_secret": {cfg.KickClientSecret},
		"refresh_token": {refreshToken},
	})
}

func postKickTokenRequest(ctx context.Context, form url.Values) (*kickTokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, kickTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("kick: token request failed: %s: %s", resp.Status, bytes.TrimSpace(data))
	}

	var tok kickTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

type kickUser struct {
	UserID         int64  `json:"user_id"`
	Name           string `json:"name"`
	Email          string `json:"email"`
	ProfilePicture string `json:"profile_picture"`
}

func fetchKickUser(ctx context.Context, accessToken string) (*kickUser, error) {
	var out struct {
		Data []kickUser `json:"data"`
	}
	if err := kickAPIRequest(ctx, http.MethodGet, kickAPIBase+"/users", accessToken, nil, &out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, errors.New("kick: no user returned for token")
	}
	return &out.Data[0], nil
}

type kickChannel struct {
	BroadcasterUserID int64  `json:"broadcaster_user_id"`
	Slug              string `json:"slug"`
}

// fetchOwnKickChannel returns the signed-in user's own channel, offered
// as a one-click choice in the picker — Kick's API has no equivalent of
// Twitch's "channels I moderate" listing, so beyond their own channel a
// user has to type the channel name manually.
func fetchOwnKickChannel(ctx context.Context, accessToken string) (*kickChannel, error) {
	var out struct {
		Data []kickChannel `json:"data"`
	}
	if err := kickAPIRequest(ctx, http.MethodGet, kickAPIBase+"/channels", accessToken, nil, &out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, errors.New("kick: no channel returned for token")
	}
	return &out.Data[0], nil
}

func resolveKickChannelBySlug(ctx context.Context, accessToken, slug string) (*kickChannel, error) {
	var out struct {
		Data []kickChannel `json:"data"`
	}
	endpoint := kickAPIBase + "/channels?" + url.Values{"slug": {slug}}.Encode()
	if err := kickAPIRequest(ctx, http.MethodGet, endpoint, accessToken, nil, &out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, fmt.Errorf("kick: channel %q not found", slug)
	}
	return &out.Data[0], nil
}

// resolveKickBroadcasterID looks up the numeric channel ID for a slug,
// as required by Kick's moderation and event-subscription endpoints.
// Results are cached on the Server for the process lifetime.
func (s *Server) resolveKickBroadcasterID(ctx context.Context, accessToken, slug string) (int64, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))

	s.kickChannelIDsMu.Lock()
	if id, ok := s.kickChannelIDs[slug]; ok {
		s.kickChannelIDsMu.Unlock()
		return id, nil
	}
	s.kickChannelIDsMu.Unlock()

	channel, err := resolveKickChannelBySlug(ctx, accessToken, slug)
	if err != nil {
		return 0, err
	}

	s.kickChannelIDsMu.Lock()
	s.kickChannelIDs[slug] = channel.BroadcasterUserID
	s.kickChannelIDsMu.Unlock()

	return channel.BroadcasterUserID, nil
}

// kickAPIError wraps a non-2xx Kick API response so callers can relay
// its status and message to the browser.
type kickAPIError struct {
	Status  int
	Message string
}

func (e *kickAPIError) Error() string {
	return fmt.Sprintf("kick api error (%d): %s", e.Status, e.Message)
}

// kickAPIRequest performs an authenticated Kick public-API request.
// Unlike Twitch, Kick needs only the bearer token — no separate
// client-ID header.
func kickAPIRequest(ctx context.Context, method, endpoint, accessToken string, body []byte, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		var envelope struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &envelope)
		if envelope.Message == "" {
			envelope.Message = resp.Status
		}
		return &kickAPIError{Status: resp.StatusCode, Message: envelope.Message}
	}

	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
