package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// handleGetTwitchEmotes returns every emote the signed-in viewer can
// actually use while chatting in the given channel — their global,
// follower, and subscriber emotes, plus that channel's own follower
// emotes if they qualify for them — for the chat composer's emote
// picker.
func (s *Server) handleGetTwitchEmotes(w http.ResponseWriter, r *http.Request) {
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

	ctx := r.Context()
	broadcasterID, err := s.resolveBroadcasterID(ctx, s.cfg.TwitchClientID, sess.AccessToken, channel)
	if err != nil {
		http.Error(w, "could not resolve channel", http.StatusBadGateway)
		return
	}

	emotes, err := fetchTwitchUserEmotes(ctx, s.cfg.TwitchClientID, sess.AccessToken, sess.UserID, broadcasterID)
	if err != nil {
		writeTwitchAPIError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(emotes)
}

// twitchUserEmotesPageLimit caps how many pages of the (cursor-paginated)
// Get User Emotes response are fetched, so an account with an unusually
// large number of emotes still returns promptly instead of the picker
// hanging on an unbounded fetch. 25 pages at 25 emotes/page (Twitch's
// per-page count for this endpoint) comfortably covers every realistic
// account.
const twitchUserEmotesPageLimit = 25

// fetchTwitchUserEmotes calls Twitch's Get User Emotes endpoint,
// following its cursor pagination, and resolves each emote to a CDN
// image URL via twitchEmoteImageURL. Requires the user:read:emotes
// scope; broadcasterID additionally includes that channel's follower
// emotes if userID qualifies for them.
// https://dev.twitch.tv/docs/api/reference/#get-user-emotes
func fetchTwitchUserEmotes(ctx context.Context, clientID, accessToken, userID, broadcasterID string) ([]chatEmoteDTO, error) {
	var (
		out    []chatEmoteDTO
		cursor string
	)
	for page := 0; page < twitchUserEmotesPageLimit; page++ {
		params := url.Values{"user_id": {userID}}
		if broadcasterID != "" {
			params.Set("broadcaster_id", broadcasterID)
		}
		if cursor != "" {
			params.Set("after", cursor)
		}

		var resp struct {
			Data []struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				EmoteType string `json:"emote_type"`
			} `json:"data"`
			Pagination struct {
				Cursor string `json:"cursor"`
			} `json:"pagination"`
		}
		if err := twitchAPIGet(ctx, clientID, accessToken, "/chat/emotes/user", params, &resp); err != nil {
			return nil, err
		}
		for _, e := range resp.Data {
			out = append(out, chatEmoteDTO{
				ID:       e.ID,
				Name:     e.Name,
				ImageURL: twitchEmoteImageURL(e.ID),
				Type:     e.EmoteType,
			})
		}

		if resp.Pagination.Cursor == "" || len(resp.Data) == 0 {
			break
		}
		cursor = resp.Pagination.Cursor
	}
	return out, nil
}
