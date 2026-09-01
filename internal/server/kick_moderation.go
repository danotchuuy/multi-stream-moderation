package server

import (
	"context"
	"encoding/json"
	"net/http"
)

// deleteKickChatMessage removes a single chat message. Unlike Twitch,
// Kick identifies the target purely by message ID in the URL path — no
// broadcaster/moderator IDs needed, since the caller's own token
// establishes both.
// https://docs.kick.com/apis/chat#delete-chat-message
func deleteKickChatMessage(ctx context.Context, accessToken, messageID string) error {
	endpoint := kickAPIBase + "/chat/" + messageID
	return kickAPIRequest(ctx, http.MethodDelete, endpoint, accessToken, nil, nil)
}

type kickBanRequest struct {
	BroadcasterUserID int64  `json:"broadcaster_user_id"`
	UserID            int64  `json:"user_id"`
	Duration          int    `json:"duration,omitempty"` // minutes, 1-10080; omitted = permanent ban
	Reason            string `json:"reason,omitempty"`
}

// banOrTimeoutKickUser bans userID outright, or times them out for
// durationMinutes if it's greater than zero. Note the unit: Kick's API
// takes minutes, where Twitch's takes seconds — the HTTP handlers keep
// each platform's native unit rather than silently converting.
// https://docs.kick.com/apis/moderation#post-moderation-bans
func banOrTimeoutKickUser(ctx context.Context, accessToken string, broadcasterUserID, userID int64, durationMinutes int, reason string) error {
	body, err := json.Marshal(kickBanRequest{
		BroadcasterUserID: broadcasterUserID,
		UserID:            userID,
		Duration:          durationMinutes,
		Reason:            reason,
	})
	if err != nil {
		return err
	}
	return kickAPIRequest(ctx, http.MethodPost, kickAPIBase+"/moderation/bans", accessToken, body, nil)
}
