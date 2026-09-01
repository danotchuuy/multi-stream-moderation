package server

import (
	"bytes"
	"context"
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
	twitchAuthorizeURL = "https://id.twitch.tv/oauth2/authorize"
	twitchTokenURL     = "https://id.twitch.tv/oauth2/token"
	twitchAPIBase      = "https://api.twitch.tv/helix"

	// twitchOAuthScopes lists the permissions requested from the user.
	//   - user:read:moderated_channels: list channels they moderate, for
	//     the channel picker.
	//   - moderator:manage:chat_messages: delete individual messages.
	//   - moderator:manage:banned_users: ban/timeout chatters.
	//   - user:write:chat: send chat messages as themselves.
	//   - channel:manage:broadcast: update their own stream's title and
	//     category. Twitch requires broadcaster_id to match the token's
	//     own user ID for this, so it only ever works for a viewer's own
	//     channel, never one they merely moderate.
	// Sessions authorized before these were added won't have them; the
	// affected calls fail with a 401 until the user reconnects their
	// Twitch account.
	twitchOAuthScopes = "user:read:moderated_channels moderator:manage:chat_messages moderator:manage:banned_users user:write:chat channel:manage:broadcast"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

type twitchTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// tokenExpiry converts a token response's expires_in into an absolute
// time, or the zero time if Twitch didn't report one (some app/token
// configurations issue non-expiring tokens).
func tokenExpiry(tok *twitchTokenResponse) time.Time {
	if tok.ExpiresIn <= 0 {
		return time.Time{}
	}
	return time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
}

func exchangeTwitchCode(ctx context.Context, cfg Config, code string) (*twitchTokenResponse, error) {
	return postTwitchTokenRequest(ctx, url.Values{
		"client_id":     {cfg.TwitchClientID},
		"client_secret": {cfg.TwitchClientSecret},
		"code":          {code},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {cfg.TwitchRedirectURL},
	})
}

// refreshTwitchToken exchanges a refresh token for a new access token,
// used to keep a persisted session alive without the user reauthorizing.
func refreshTwitchToken(ctx context.Context, cfg Config, refreshToken string) (*twitchTokenResponse, error) {
	return postTwitchTokenRequest(ctx, url.Values{
		"client_id":     {cfg.TwitchClientID},
		"client_secret": {cfg.TwitchClientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	})
}

func postTwitchTokenRequest(ctx context.Context, form url.Values) (*twitchTokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, twitchTokenURL, strings.NewReader(form.Encode()))
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
		return nil, fmt.Errorf("twitch: token request failed: %s", resp.Status)
	}

	var tok twitchTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

type twitchUser struct {
	ID              string `json:"id"`
	Login           string `json:"login"`
	DisplayName     string `json:"display_name"`
	ProfileImageURL string `json:"profile_image_url"`
}

func fetchTwitchUser(ctx context.Context, clientID, accessToken string) (*twitchUser, error) {
	var out struct {
		Data []twitchUser `json:"data"`
	}
	if err := twitchAPIGet(ctx, clientID, accessToken, "/users", nil, &out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, errors.New("twitch: no user returned for token")
	}
	return &out.Data[0], nil
}

type moderatedChannel struct {
	BroadcasterID    string `json:"broadcaster_id"`
	BroadcasterLogin string `json:"broadcaster_login"`
	BroadcasterName  string `json:"broadcaster_name"`
}

func fetchModeratedChannels(ctx context.Context, clientID, accessToken, userID string) ([]moderatedChannel, error) {
	var out struct {
		Data []moderatedChannel `json:"data"`
	}
	params := url.Values{"user_id": {userID}, "first": {"100"}}
	if err := twitchAPIGet(ctx, clientID, accessToken, "/moderation/channels", params, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func twitchAPIGet(ctx context.Context, clientID, accessToken, path string, params url.Values, out any) error {
	u := twitchAPIBase + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	return twitchAPIRequest(ctx, http.MethodGet, u, clientID, accessToken, nil, out)
}

// twitchAPIError wraps a non-2xx Helix response so callers (in
// particular the moderation handlers) can relay Twitch's own status and
// message to the browser instead of a generic failure.
type twitchAPIError struct {
	Status  int
	Message string
}

func (e *twitchAPIError) Error() string {
	return fmt.Sprintf("twitch api error (%d): %s", e.Status, e.Message)
}

// twitchAPIRequest performs an authenticated Helix request. body, if
// non-nil, is sent as the JSON request body; out, if non-nil, receives
// the decoded JSON response body.
func twitchAPIRequest(ctx context.Context, method, endpoint, clientID, accessToken string, body []byte, out any) error {
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
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Client-Id", clientID)

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		var apiErr struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &apiErr)
		if apiErr.Message == "" {
			apiErr.Message = resp.Status
		}
		return &twitchAPIError{Status: resp.StatusCode, Message: apiErr.Message}
	}

	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
