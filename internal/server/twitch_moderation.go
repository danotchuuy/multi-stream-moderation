package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// resolveBroadcasterID looks up the numeric channel ID for a login name,
// as required by Twitch's moderation endpoints. Results are cached on
// the Server for the process lifetime since a channel's ID never
// changes.
func (s *Server) resolveBroadcasterID(ctx context.Context, clientID, accessToken, login string) (string, error) {
	login = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(login), "#"))

	s.channelIDsMu.Lock()
	if id, ok := s.channelIDs[login]; ok {
		s.channelIDsMu.Unlock()
		return id, nil
	}
	s.channelIDsMu.Unlock()

	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := twitchAPIGet(ctx, clientID, accessToken, "/users", url.Values{"login": {login}}, &out); err != nil {
		return "", err
	}
	if len(out.Data) == 0 {
		return "", fmt.Errorf("twitch: channel %q not found", login)
	}
	id := out.Data[0].ID

	s.channelIDsMu.Lock()
	s.channelIDs[login] = id
	s.channelIDsMu.Unlock()

	return id, nil
}

// deleteChatMessage removes a single chat message. moderatorID must be
// the authenticated user's own ID; Twitch checks that they're actually a
// moderator (or the broadcaster) of broadcasterID.
// https://dev.twitch.tv/docs/api/reference/#delete-chat-messages
func deleteChatMessage(ctx context.Context, clientID, accessToken, broadcasterID, moderatorID, messageID string) error {
	params := url.Values{
		"broadcaster_id": {broadcasterID},
		"moderator_id":   {moderatorID},
		"message_id":     {messageID},
	}
	endpoint := twitchAPIBase + "/moderation/chat?" + params.Encode()
	return twitchAPIRequest(ctx, http.MethodDelete, endpoint, clientID, accessToken, nil, nil)
}

// banUserRequest is the Ban Users request body. Unlike most Helix
// endpoints, "data" here is a single object, not an array — sending an
// array (as if batching) silently drops user_id from Twitch's point of
// view, surfacing as `Missing required parameter "user_id"`.
type banUserRequest struct {
	Data banUserEntry `json:"data"`
}

type banUserEntry struct {
	UserID   string `json:"user_id"`
	Duration int    `json:"duration,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// banOrTimeoutUser bans userID outright, or times them out for
// durationSeconds if it's greater than zero.
// https://dev.twitch.tv/docs/api/reference/#ban-user
func banOrTimeoutUser(ctx context.Context, clientID, accessToken, broadcasterID, moderatorID, userID string, durationSeconds int, reason string) error {
	body, err := json.Marshal(banUserRequest{Data: banUserEntry{
		UserID:   userID,
		Duration: durationSeconds,
		Reason:   reason,
	}})
	if err != nil {
		return err
	}

	params := url.Values{
		"broadcaster_id": {broadcasterID},
		"moderator_id":   {moderatorID},
	}
	endpoint := twitchAPIBase + "/moderation/bans?" + params.Encode()
	return twitchAPIRequest(ctx, http.MethodPost, endpoint, clientID, accessToken, body, nil)
}
