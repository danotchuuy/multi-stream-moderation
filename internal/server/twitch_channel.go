package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// twitchChannelInfo is the subset of Get Channel Information's response
// this app cares about.
// https://dev.twitch.tv/docs/api/reference/#get-channel-information
type twitchChannelInfo struct {
	Title    string `json:"title"`
	GameID   string `json:"game_id"`
	GameName string `json:"game_name"`
}

func getChannelInfo(ctx context.Context, clientID, accessToken, broadcasterID string) (*twitchChannelInfo, error) {
	var out struct {
		Data []twitchChannelInfo `json:"data"`
	}
	if err := twitchAPIGet(ctx, clientID, accessToken, "/channels", url.Values{"broadcaster_id": {broadcasterID}}, &out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, fmt.Errorf("twitch: no channel info returned for %s", broadcasterID)
	}
	return &out.Data[0], nil
}

type updateChannelInfoRequest struct {
	Title  *string `json:"title,omitempty"`
	GameID *string `json:"game_id,omitempty"`
}

// updateChannelInfo updates a channel's title and/or category (game).
// Twitch only lets the broadcaster themselves call this — broadcasterID
// must match the calling token's own user ID, or the request 401s, so a
// moderator viewing someone else's channel can't use this even though
// they can moderate its chat.
// https://dev.twitch.tv/docs/api/reference/#modify-channel-information
func updateChannelInfo(ctx context.Context, clientID, accessToken, broadcasterID string, title, gameID *string) error {
	body, err := json.Marshal(updateChannelInfoRequest{Title: title, GameID: gameID})
	if err != nil {
		return err
	}
	endpoint := twitchAPIBase + "/channels?" + url.Values{"broadcaster_id": {broadcasterID}}.Encode()
	return twitchAPIRequest(ctx, http.MethodPatch, endpoint, clientID, accessToken, body, nil)
}

type twitchCategory struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	BoxArtURL string `json:"box_art_url"`
}

// searchTwitchCategories looks up categories/games by (partial) name,
// for the stream-info editor's category picker.
// https://dev.twitch.tv/docs/api/reference/#search-categories
func searchTwitchCategories(ctx context.Context, clientID, accessToken, query string) ([]twitchCategory, error) {
	var out struct {
		Data []twitchCategory `json:"data"`
	}
	params := url.Values{"query": {query}, "first": {"20"}}
	if err := twitchAPIGet(ctx, clientID, accessToken, "/search/categories", params, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// twitchBoxArtURL fills in the {width}x{height} placeholders Twitch
// leaves in a category's box_art_url template.
func twitchBoxArtURL(template string, width, height int) string {
	r := strings.NewReplacer("{width}", fmt.Sprint(width), "{height}", fmt.Sprint(height))
	return r.Replace(template)
}

// --- HTTP handlers -----------------------------------------------------

type twitchChannelInfoDTO struct {
	Title        string `json:"title"`
	CategoryID   string `json:"categoryId"`
	CategoryName string `json:"categoryName"`
}

// handleGetTwitchChannelInfo returns a channel's current title and
// category, for the stream-info editor to prefill.
func (s *Server) handleGetTwitchChannelInfo(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	channel, err := normalizeTwitchChannel(r.URL.Query().Get("channel"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	broadcasterID, err := s.resolveBroadcasterID(r.Context(), s.cfg.TwitchClientID, sess.AccessToken, channel)
	if err != nil {
		http.Error(w, "could not resolve channel", http.StatusBadGateway)
		return
	}

	info, err := getChannelInfo(r.Context(), s.cfg.TwitchClientID, sess.AccessToken, broadcasterID)
	if err != nil {
		writeTwitchAPIError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(twitchChannelInfoDTO{
		Title:        info.Title,
		CategoryID:   info.GameID,
		CategoryName: info.GameName,
	})
}

type updateTwitchChannelInfoRequestDTO struct {
	Channel    string  `json:"channel"`
	Title      *string `json:"title,omitempty"`
	CategoryID *string `json:"categoryId,omitempty"`
}

// handleUpdateTwitchChannelInfo updates the channel's title and/or
// category. Only the broadcaster can succeed here — see updateChannelInfo.
func (s *Server) handleUpdateTwitchChannelInfo(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	var req updateTwitchChannelInfoRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Title == nil && req.CategoryID == nil {
		http.Error(w, "title or categoryId is required", http.StatusBadRequest)
		return
	}
	channel, err := normalizeTwitchChannel(req.Channel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	broadcasterID, err := s.resolveBroadcasterID(r.Context(), s.cfg.TwitchClientID, sess.AccessToken, channel)
	if err != nil {
		http.Error(w, "could not resolve channel", http.StatusBadGateway)
		return
	}

	if err := updateChannelInfo(r.Context(), s.cfg.TwitchClientID, sess.AccessToken, broadcasterID, req.Title, req.CategoryID); err != nil {
		writeTwitchAPIError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type twitchCategoryDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Image string `json:"image"`
}

type twitchCategoriesResponseDTO struct {
	Categories []twitchCategoryDTO `json:"categories"`
}

// handleSearchTwitchCategories looks up categories by name, for the
// stream-info editor's category picker.
func (s *Server) handleSearchTwitchCategories(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("query"))
	w.Header().Set("Content-Type", "application/json")
	if query == "" {
		json.NewEncoder(w).Encode(twitchCategoriesResponseDTO{})
		return
	}

	categories, err := searchTwitchCategories(r.Context(), s.cfg.TwitchClientID, sess.AccessToken, query)
	if err != nil {
		writeTwitchAPIError(w, err)
		return
	}
	out := make([]twitchCategoryDTO, len(categories))
	for i, c := range categories {
		out[i] = twitchCategoryDTO{ID: c.ID, Name: c.Name, Image: twitchBoxArtURL(c.BoxArtURL, 52, 72)}
	}
	json.NewEncoder(w).Encode(twitchCategoriesResponseDTO{Categories: out})
}
