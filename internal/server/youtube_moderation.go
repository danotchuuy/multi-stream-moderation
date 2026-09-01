package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

// deleteYouTubeChatMessage removes a single chat message.
// https://developers.google.com/youtube/v3/live/docs/liveChatMessages/delete
func deleteYouTubeChatMessage(ctx context.Context, accessToken, messageID string) error {
	endpoint := youtubeAPIBase + "/liveChat/messages?" + url.Values{"id": {messageID}}.Encode()
	return youtubeAPIRequest(ctx, http.MethodDelete, endpoint, accessToken, nil, nil)
}

type liveChatBanRequest struct {
	Snippet liveChatBanSnippet `json:"snippet"`
}

type liveChatBanSnippet struct {
	LiveChatID        string                   `json:"liveChatId"`
	Type              string                   `json:"type"` // "permanent" or "temporary"
	BannedUserDetails liveChatBannedUserDetail `json:"bannedUserDetails"`
	// BanDurationSeconds is a string in YouTube's schema (a uint64
	// represented as JSON string to avoid precision loss) — sending it
	// as a JSON number is the same class of bug as Kick's ban endpoint
	// wanting an object where an array seemed natural: it will silently
	// fail to bind. Only set for temporary bans.
	BanDurationSeconds string `json:"banDurationSeconds,omitempty"`
}

type liveChatBannedUserDetail struct {
	ChannelID string `json:"channelId"`
}

// banOrTimeoutYouTubeUser bans channelID outright, or times them out for
// durationSeconds if it's greater than zero.
// https://developers.google.com/youtube/v3/live/docs/liveChatBans/insert
func banOrTimeoutYouTubeUser(ctx context.Context, accessToken, liveChatID, channelID string, durationSeconds int) error {
	snippet := liveChatBanSnippet{
		LiveChatID:        liveChatID,
		Type:              "permanent",
		BannedUserDetails: liveChatBannedUserDetail{ChannelID: channelID},
	}
	if durationSeconds > 0 {
		snippet.Type = "temporary"
		snippet.BanDurationSeconds = strconv.Itoa(durationSeconds)
	}

	body, err := json.Marshal(liveChatBanRequest{Snippet: snippet})
	if err != nil {
		return err
	}

	endpoint := youtubeAPIBase + "/liveChat/bans?" + url.Values{"part": {"snippet"}}.Encode()
	return youtubeAPIRequest(ctx, http.MethodPost, endpoint, accessToken, body, nil)
}
