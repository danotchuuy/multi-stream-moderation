package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

// kickEmotesURL is Kick's own web client's emote-listing endpoint. Kick's
// official public API (api.kick.com) has no equivalent — this is an
// undocumented, unauthenticated endpoint the kick.com website itself
// calls, so it could change or disappear without notice; it's the only
// way to offer a Kick emote picker at all today.
const kickEmotesURL = "https://kick.com/emotes/"

// handleGetKickEmotes returns the channel's own emotes plus Kick's
// global and "Kick Emoji" sets, for the chat composer's emote picker.
// Unlike Twitch's equivalent, this isn't scoped to what the signed-in
// viewer can personally use — Kick's endpoint returns a channel's full
// emote set regardless of the viewer's subscription status — so
// subscriber-only emotes are flagged (chatEmoteDTO.Locked) rather than
// filtered out.
func (s *Server) handleGetKickEmotes(w http.ResponseWriter, r *http.Request) {
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

	emotes, err := fetchKickChannelEmotes(r.Context(), channel)
	if err != nil {
		http.Error(w, "could not load kick emotes", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(emotes)
}

// kickEmoteSetDTO mirrors one entry of the array kick.com/emotes/<slug>
// returns. The channel's own emote set is the one with Slug set (its
// "id" is the channel's numeric ID, not usable as a set discriminator);
// Kick's shared sets instead carry Name ("Global" or "Emojis") with no
// Slug, so id/name/slug can't share one consistently-typed field.
type kickEmoteSetDTO struct {
	Name   string `json:"name"`
	Slug   string `json:"slug"`
	Emotes []struct {
		ID              int64  `json:"id"`
		Name            string `json:"name"`
		SubscribersOnly bool   `json:"subscribers_only"`
	} `json:"emotes"`
}

// fetchKickChannelEmotes fetches slug's emote sets and flattens them
// into the shared chatEmoteDTO shape, tagging each with which set it
// came from ("channel", "global", or "emoji").
func fetchKickChannelEmotes(ctx context.Context, slug string) ([]chatEmoteDTO, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, kickEmotesURL+slug, nil)
	if err != nil {
		return nil, err
	}
	// Kick's Cloudflare front door can reject requests with no
	// browser-like User-Agent at all; this endpoint is otherwise public
	// and needs no authentication.
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; multi-stream-moderation)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("kick: emotes request failed: " + resp.Status)
	}

	var sets []kickEmoteSetDTO
	if err := json.NewDecoder(resp.Body).Decode(&sets); err != nil {
		return nil, err
	}

	var out []chatEmoteDTO
	for _, set := range sets {
		setType := "channel"
		if set.Slug == "" {
			switch set.Name {
			case "Global":
				setType = "global"
			case "Emojis":
				setType = "emoji"
			default:
				continue
			}
		}
		for _, e := range set.Emotes {
			id := strconv.FormatInt(e.ID, 10)
			out = append(out, chatEmoteDTO{
				ID:       id,
				Name:     e.Name,
				ImageURL: kickEmoteImageURL(id),
				Type:     setType,
				Locked:   e.SubscribersOnly,
			})
		}
	}
	return out, nil
}
