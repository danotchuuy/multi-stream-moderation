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
	"regexp"
	"strings"
	"time"
)

const (
	youtubeAuthorizeURL = "https://accounts.google.com/o/oauth2/v2/auth"
	youtubeTokenURL     = "https://oauth2.googleapis.com/token"
	youtubeAPIBase      = "https://youtube.googleapis.com/youtube/v3"

	// youtubeOAuthScope covers both reading live chat and moderating it
	// (delete/ban) — youtube.readonly alone isn't enough for the write
	// calls, so there's no benefit to requesting a narrower scope too.
	youtubeOAuthScope = "https://www.googleapis.com/auth/youtube.force-ssl"
)

type youtubeTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func youtubeTokenExpiry(tok *youtubeTokenResponse) time.Time {
	if tok.ExpiresIn <= 0 {
		return time.Time{}
	}
	return time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
}

func exchangeYouTubeCode(ctx context.Context, cfg Config, code string) (*youtubeTokenResponse, error) {
	return postYouTubeTokenRequest(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {cfg.YouTubeClientID},
		"client_secret": {cfg.YouTubeClientSecret},
		"redirect_uri":  {cfg.YouTubeRedirectURL},
		"code":          {code},
	})
}

// refreshYouTubeToken exchanges a refresh token for a new access token.
// Google normally does not return a new refresh_token here — the
// original keeps working — so callers should only overwrite it if one
// actually comes back.
func refreshYouTubeToken(ctx context.Context, cfg Config, refreshToken string) (*youtubeTokenResponse, error) {
	return postYouTubeTokenRequest(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {cfg.YouTubeClientID},
		"client_secret": {cfg.YouTubeClientSecret},
		"refresh_token": {refreshToken},
	})
}

func postYouTubeTokenRequest(ctx context.Context, form url.Values) (*youtubeTokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, youtubeTokenURL, strings.NewReader(form.Encode()))
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
		return nil, fmt.Errorf("youtube: token request failed: %s: %s", resp.Status, bytes.TrimSpace(data))
	}

	var tok youtubeTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

type youtubeChannel struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Image string `json:"image"`
}

// fetchOwnYouTubeChannel identifies the signed-in user via channels.list
// mine=true.
func fetchOwnYouTubeChannel(ctx context.Context, accessToken string) (*youtubeChannel, error) {
	var out struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet struct {
				Title      string `json:"title"`
				Thumbnails struct {
					Default struct {
						URL string `json:"url"`
					} `json:"default"`
				} `json:"thumbnails"`
			} `json:"snippet"`
		} `json:"items"`
	}
	params := url.Values{"part": {"snippet"}, "mine": {"true"}}
	if err := youtubeAPIRequest(ctx, http.MethodGet, youtubeAPIBase+"/channels?"+params.Encode(), accessToken, nil, &out); err != nil {
		return nil, err
	}
	if len(out.Items) == 0 {
		return nil, errors.New("youtube: no channel returned for token")
	}
	item := out.Items[0]
	return &youtubeChannel{ID: item.ID, Title: item.Snippet.Title, Image: item.Snippet.Thumbnails.Default.URL}, nil
}

// resolveOwnActiveLiveChat finds the signed-in user's own currently
// active broadcast and returns its live chat ID directly — cheaper than
// videos.list since liveBroadcasts already carries it.
func resolveOwnActiveLiveChat(ctx context.Context, accessToken string) (liveChatID string, err error) {
	var out struct {
		Items []struct {
			Snippet struct {
				LiveChatID string `json:"liveChatId"`
			} `json:"snippet"`
		} `json:"items"`
	}
	params := url.Values{
		"part":            {"snippet"},
		"mine":            {"true"},
		"broadcastStatus": {"active"},
		"broadcastType":   {"all"},
	}
	if err := youtubeAPIRequest(ctx, http.MethodGet, youtubeAPIBase+"/liveBroadcasts?"+params.Encode(), accessToken, nil, &out); err != nil {
		return "", err
	}
	for _, item := range out.Items {
		if item.Snippet.LiveChatID != "" {
			return item.Snippet.LiveChatID, nil
		}
	}
	return "", errors.New("youtube: no active live broadcast found for your channel")
}

// resolveVideoLiveChat looks up a specific video's active live chat ID,
// along with the broadcasting channel's ID (needed to tell whether the
// viewer is that channel's owner) and title (shown in the chat panel's
// header). videos.list's snippet already carries the channel's title
// directly, so this needs no separate channels.list call to get it.
func resolveVideoLiveChat(ctx context.Context, accessToken, videoID string) (liveChatID, channelID, channelTitle string, err error) {
	var out struct {
		Items []struct {
			Snippet struct {
				ChannelID    string `json:"channelId"`
				ChannelTitle string `json:"channelTitle"`
			} `json:"snippet"`
			LiveStreamingDetails struct {
				ActiveLiveChatID string `json:"activeLiveChatId"`
			} `json:"liveStreamingDetails"`
		} `json:"items"`
	}
	params := url.Values{"part": {"snippet,liveStreamingDetails"}, "id": {videoID}}
	if err := youtubeAPIRequest(ctx, http.MethodGet, youtubeAPIBase+"/videos?"+params.Encode(), accessToken, nil, &out); err != nil {
		return "", "", "", err
	}
	if len(out.Items) == 0 {
		return "", "", "", fmt.Errorf("youtube: video %q not found", videoID)
	}
	item := out.Items[0]
	if item.LiveStreamingDetails.ActiveLiveChatID == "" {
		return "", "", "", errors.New("youtube: that video has no active live chat (it may not be live)")
	}
	return item.LiveStreamingDetails.ActiveLiveChatID, item.Snippet.ChannelID, item.Snippet.ChannelTitle, nil
}

// youtubeVideoIDPattern matches an 11-character YouTube video ID, the
// same alphabet YouTube itself uses (base64url-ish: letters, digits,
// - and _).
var youtubeVideoIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// parseYouTubeVideoID accepts either a bare video ID or a full YouTube
// URL (watch, live, youtu.be, or shorts forms) and returns just the ID.
func parseYouTubeVideoID(input string) (string, error) {
	input = strings.TrimSpace(input)
	if youtubeVideoIDPattern.MatchString(input) {
		return input, nil
	}

	u, err := url.Parse(input)
	if err == nil && u.Host != "" {
		host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
		switch host {
		case "youtu.be":
			id := strings.Trim(u.Path, "/")
			if youtubeVideoIDPattern.MatchString(id) {
				return id, nil
			}
		case "youtube.com", "m.youtube.com", "music.youtube.com":
			if id := u.Query().Get("v"); youtubeVideoIDPattern.MatchString(id) {
				return id, nil
			}
			for _, prefix := range []string{"/live/", "/shorts/", "/embed/"} {
				if strings.HasPrefix(u.Path, prefix) {
					id := strings.TrimPrefix(u.Path, prefix)
					id = strings.SplitN(id, "/", 2)[0]
					if youtubeVideoIDPattern.MatchString(id) {
						return id, nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("could not find a YouTube video ID in %q", input)
}

// youtubeAPIError wraps a non-2xx YouTube Data API response so callers
// can relay its status and message to the browser.
type youtubeAPIError struct {
	Status  int
	Message string
}

func (e *youtubeAPIError) Error() string {
	return fmt.Sprintf("youtube api error (%d): %s", e.Status, e.Message)
}

// youtubeAPIRequest performs an authenticated YouTube Data API request.
func youtubeAPIRequest(ctx context.Context, method, endpoint, accessToken string, body []byte, out any) error {
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

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		var envelope struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &envelope)
		msg := envelope.Error.Message
		if msg == "" {
			msg = resp.Status
		}
		return &youtubeAPIError{Status: resp.StatusCode, Message: msg}
	}

	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
