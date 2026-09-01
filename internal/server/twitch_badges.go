package server

import (
	"context"
	"net/url"
)

// twitchBadgeCatalog resolves a chat badge (set ID and version ID, as
// carried on an EventSub chat message) to the exact image URL Twitch
// itself shows for it — so a viewer's badges render the same way here
// as they do on twitch.tv, rather than an approximation.
type twitchBadgeCatalog map[string]map[string]string

// imageURL returns setID/versionID's image, or "" if this catalog has
// no such badge (an unrecognized or since-removed one) — the frontend
// falls back to a generic icon in that case.
func (c twitchBadgeCatalog) imageURL(setID, versionID string) string {
	return c[setID][versionID]
}

// twitchBadgeCatalogFor returns broadcasterID's badge catalog, fetching
// and caching it on first use. Results are cached on the Server for the
// process lifetime, the same way resolveBroadcasterID caches channel
// IDs — badge art essentially never changes mid-stream, so there's no
// reason to refetch it on every chat connection.
func (s *Server) twitchBadgeCatalogFor(ctx context.Context, clientID, accessToken, broadcasterID string) (twitchBadgeCatalog, error) {
	s.twitchBadgesMu.Lock()
	if catalog, ok := s.twitchBadges[broadcasterID]; ok {
		s.twitchBadgesMu.Unlock()
		return catalog, nil
	}
	s.twitchBadgesMu.Unlock()

	catalog, err := fetchTwitchBadgeCatalog(ctx, clientID, accessToken, broadcasterID)
	if err != nil {
		return nil, err
	}

	s.twitchBadgesMu.Lock()
	s.twitchBadges[broadcasterID] = catalog
	s.twitchBadgesMu.Unlock()
	return catalog, nil
}

// fetchTwitchBadgeCatalog fetches the badge images Twitch shows for
// broadcasterID: its own custom badge sets (e.g. that channel's own
// subscriber badge art) layered over Twitch's global ones (moderator,
// VIP, Turbo, Prime, ...) — matching how Twitch itself resolves a
// chatter's badges, where a channel's own set takes precedence.
// https://dev.twitch.tv/docs/api/reference/#get-global-chat-badges
// https://dev.twitch.tv/docs/api/reference/#get-channel-chat-badges
func fetchTwitchBadgeCatalog(ctx context.Context, clientID, accessToken, broadcasterID string) (twitchBadgeCatalog, error) {
	catalog := make(twitchBadgeCatalog)
	if err := loadTwitchBadgeSets(ctx, clientID, accessToken, "/chat/badges/global", nil, catalog); err != nil {
		return nil, err
	}
	params := url.Values{"broadcaster_id": {broadcasterID}}
	if err := loadTwitchBadgeSets(ctx, clientID, accessToken, "/chat/badges", params, catalog); err != nil {
		return nil, err
	}
	return catalog, nil
}

// loadTwitchBadgeSets fetches one badge-sets endpoint and merges its
// versions into into, keyed by set ID then version ID. Called once for
// the global set and once for a channel's own, in that order, so the
// channel's own art naturally overrides the global entry when both
// define the same set/version (which the API otherwise never does on
// its own).
func loadTwitchBadgeSets(ctx context.Context, clientID, accessToken, path string, params url.Values, into twitchBadgeCatalog) error {
	var out struct {
		Data []struct {
			SetID    string `json:"set_id"`
			Versions []struct {
				ID         string `json:"id"`
				ImageURL2x string `json:"image_url_2x"`
			} `json:"versions"`
		} `json:"data"`
	}
	if err := twitchAPIGet(ctx, clientID, accessToken, path, params, &out); err != nil {
		return err
	}
	for _, set := range out.Data {
		if into[set.SetID] == nil {
			into[set.SetID] = make(map[string]string)
		}
		for _, v := range set.Versions {
			into[set.SetID][v.ID] = v.ImageURL2x
		}
	}
	return nil
}
