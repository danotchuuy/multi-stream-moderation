package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// kickAPIBaseV2 hosts Kick's newer public-API endpoints (currently just
// category listing/search) that haven't been added under the v1 base
// other endpoints in this app use.
const kickAPIBaseV2 = "https://api.kick.com/public/v2"

// kickChannelDetail is the subset of Get Channels' response this app
// cares about.
// https://docs.kick.com/apis/channels#get-channels
type kickChannelDetail struct {
	StreamTitle string `json:"stream_title"`
	Category    struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		Thumbnail string `json:"thumbnail"`
	} `json:"category"`
}

// fetchKickChannelDetail reads a channel's current stream title and
// category by slug.
func fetchKickChannelDetail(ctx context.Context, accessToken, slug string) (*kickChannelDetail, error) {
	var out struct {
		Data []kickChannelDetail `json:"data"`
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

type kickUpdateChannelRequest struct {
	StreamTitle *string `json:"stream_title,omitempty"`
	CategoryID  *int64  `json:"category_id,omitempty"`
}

// updateKickChannel updates the caller's own channel's stream title
// and/or category — Kick identifies the channel purely from the access
// token, with no broadcaster ID in the request, so (unlike Twitch) this
// can never accidentally target a different channel than the one the
// signed-in user owns.
// https://docs.kick.com/apis/channels#patch-channels
func updateKickChannel(ctx context.Context, accessToken string, title *string, categoryID *int64) error {
	body, err := json.Marshal(kickUpdateChannelRequest{StreamTitle: title, CategoryID: categoryID})
	if err != nil {
		return err
	}
	return kickAPIRequest(ctx, http.MethodPatch, kickAPIBase+"/channels", accessToken, body, nil)
}

type kickCategory struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Thumbnail string `json:"thumbnail"`
}

// searchKickCategories looks up categories by (partial) name. Kick
// requires at least 3 characters; the HTTP handler below only calls this
// once the query already meets that bar.
// https://docs.kick.com/apis/categories#get-categories
func searchKickCategories(ctx context.Context, accessToken, query string) ([]kickCategory, error) {
	var out struct {
		Data []kickCategory `json:"data"`
	}
	endpoint := kickAPIBaseV2 + "/categories?" + url.Values{"name": {query}}.Encode()
	if err := kickAPIRequest(ctx, http.MethodGet, endpoint, accessToken, nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// --- HTTP handlers -----------------------------------------------------

type kickChannelInfoDTO struct {
	Title        string `json:"title"`
	CategoryID   int64  `json:"categoryId"`
	CategoryName string `json:"categoryName"`
}

// handleGetKickChannelInfo returns a channel's current title and
// category, for the stream-info editor to prefill.
func (s *Server) handleGetKickChannelInfo(w http.ResponseWriter, r *http.Request) {
	sess := s.kickSessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	channel, err := normalizeKickChannel(r.URL.Query().Get("channel"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	detail, err := fetchKickChannelDetail(r.Context(), sess.AccessToken, channel)
	if err != nil {
		writeKickAPIError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(kickChannelInfoDTO{
		Title:        detail.StreamTitle,
		CategoryID:   detail.Category.ID,
		CategoryName: detail.Category.Name,
	})
}

type updateKickChannelRequestDTO struct {
	Title      *string `json:"title,omitempty"`
	CategoryID *int64  `json:"categoryId,omitempty"`
}

// handleUpdateKickChannelInfo updates the signed-in user's own channel's
// title and/or category.
func (s *Server) handleUpdateKickChannelInfo(w http.ResponseWriter, r *http.Request) {
	sess := s.kickSessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	var req updateKickChannelRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Title == nil && req.CategoryID == nil {
		http.Error(w, "title or categoryId is required", http.StatusBadRequest)
		return
	}

	if err := updateKickChannel(r.Context(), sess.AccessToken, req.Title, req.CategoryID); err != nil {
		writeKickAPIError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type kickCategoryDTO struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Image string `json:"image"`
}

type kickCategoriesResponseDTO struct {
	Categories []kickCategoryDTO `json:"categories"`
}

// handleSearchKickCategories looks up categories by name, for the
// stream-info editor's category picker.
func (s *Server) handleSearchKickCategories(w http.ResponseWriter, r *http.Request) {
	sess := s.kickSessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("query"))
	w.Header().Set("Content-Type", "application/json")
	if len(query) < 3 {
		json.NewEncoder(w).Encode(kickCategoriesResponseDTO{})
		return
	}

	categories, err := searchKickCategories(r.Context(), sess.AccessToken, query)
	if err != nil {
		writeKickAPIError(w, err)
		return
	}
	out := make([]kickCategoryDTO, len(categories))
	for i, c := range categories {
		out[i] = kickCategoryDTO{ID: c.ID, Name: c.Name, Image: c.Thumbnail}
	}
	json.NewEncoder(w).Encode(kickCategoriesResponseDTO{Categories: out})
}
