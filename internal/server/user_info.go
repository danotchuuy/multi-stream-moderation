package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// userInfoDTO is what the user card shows beyond what a chat message
// already carries. CreatedAt is the account's creation time in Unix
// milliseconds.
type userInfoDTO struct {
	ID           string `json:"id"`
	DisplayName  string `json:"displayName"`
	ProfileImage string `json:"profileImage,omitempty"`
	CreatedAt    int64  `json:"createdAt"`
}

// userInfoCache keeps user lookups for the life of the process. An
// account's creation date never changes, and the same few chatters tend
// to be looked up repeatedly during one stream.
type userInfoCache struct {
	mu      sync.Mutex
	entries map[string]userInfoDTO
}

func newUserInfoCache() *userInfoCache {
	return &userInfoCache{entries: make(map[string]userInfoDTO)}
}

func (c *userInfoCache) get(key string) (userInfoDTO, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.entries[key]
	return v, ok
}

func (c *userInfoCache) set(key string, v userInfoDTO) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = v
}

func parseRFC3339Millis(s string) int64 {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

// fetchTwitchUserInfo looks up a Twitch user by ID. Needs no scope
// beyond a valid user token.
// https://dev.twitch.tv/docs/api/reference/#get-users
func fetchTwitchUserInfo(ctx context.Context, clientID, accessToken, userID string) (*userInfoDTO, error) {
	var out struct {
		Data []struct {
			ID              string `json:"id"`
			DisplayName     string `json:"display_name"`
			ProfileImageURL string `json:"profile_image_url"`
			CreatedAt       string `json:"created_at"`
		} `json:"data"`
	}
	if err := twitchAPIGet(ctx, clientID, accessToken, "/users", url.Values{"id": {userID}}, &out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	u := out.Data[0]
	return &userInfoDTO{
		ID:           u.ID,
		DisplayName:  u.DisplayName,
		ProfileImage: u.ProfileImageURL,
		CreatedAt:    parseRFC3339Millis(u.CreatedAt),
	}, nil
}

// fetchYouTubeChannelInfo looks up a YouTube channel by ID. Costs 1
// quota unit, hence the cache in front of it.
// https://developers.google.com/youtube/v3/docs/channels/list
func fetchYouTubeChannelInfo(ctx context.Context, accessToken, channelID string) (*userInfoDTO, error) {
	endpoint := youtubeAPIBase + "/channels?" + url.Values{"part": {"snippet"}, "id": {channelID}}.Encode()
	var out struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet struct {
				Title       string `json:"title"`
				PublishedAt string `json:"publishedAt"`
				Thumbnails  struct {
					Default struct {
						URL string `json:"url"`
					} `json:"default"`
				} `json:"thumbnails"`
			} `json:"snippet"`
		} `json:"items"`
	}
	if err := youtubeAPIRequest(ctx, http.MethodGet, endpoint, accessToken, nil, &out); err != nil {
		return nil, err
	}
	if len(out.Items) == 0 {
		return nil, nil
	}
	c := out.Items[0]
	return &userInfoDTO{
		ID:           c.ID,
		DisplayName:  c.Snippet.Title,
		ProfileImage: c.Snippet.Thumbnails.Default.URL,
		CreatedAt:    parseRFC3339Millis(c.Snippet.PublishedAt),
	}, nil
}

// handleGetTwitchUser returns account info for the user card.
func (s *Server) handleGetTwitchUser(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	userID := strings.TrimSpace(r.PathValue("id"))
	if userID == "" {
		http.Error(w, "user ID is required", http.StatusBadRequest)
		return
	}

	key := "twitch:" + userID
	if info, ok := s.userInfo.get(key); ok {
		writeJSON(w, info)
		return
	}
	info, err := fetchTwitchUserInfo(r.Context(), s.cfg.TwitchClientID, sess.AccessToken, userID)
	if err != nil {
		writeTwitchAPIError(w, err)
		return
	}
	if info == nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	s.userInfo.set(key, *info)
	writeJSON(w, info)
}

// handleGetYouTubeUser returns channel info for the user card.
func (s *Server) handleGetYouTubeUser(w http.ResponseWriter, r *http.Request) {
	sess := s.youtubeSessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	channelID := strings.TrimSpace(r.PathValue("id"))
	if channelID == "" {
		http.Error(w, "channel ID is required", http.StatusBadRequest)
		return
	}

	key := "youtube:" + channelID
	if info, ok := s.userInfo.get(key); ok {
		writeJSON(w, info)
		return
	}
	info, err := fetchYouTubeChannelInfo(r.Context(), sess.AccessToken, channelID)
	if err != nil {
		writeYouTubeError(w, err, http.StatusBadGateway)
		return
	}
	if info == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	s.userInfo.set(key, *info)
	writeJSON(w, info)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
