package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// deleteYouTubeChatMessage removes a single chat message.
// https://developers.google.com/youtube/v3/live/docs/liveChatMessages/delete
func deleteYouTubeChatMessage(ctx context.Context, accessToken, messageID string) error {
	endpoint := youtubeAPIBase + "/liveChat/messages?" + url.Values{"id": {messageID}}.Encode()
	return youtubeAPIRequest(ctx, http.MethodDelete, endpoint, accessToken, nil, nil)
}

type liveChatSendMessageRequest struct {
	Snippet liveChatSendMessageSnippet `json:"snippet"`
}

type liveChatSendMessageSnippet struct {
	LiveChatID         string                        `json:"liveChatId"`
	Type               string                        `json:"type"`
	TextMessageDetails liveChatSendMessageTextDetail `json:"textMessageDetails"`
}

type liveChatSendMessageTextDetail struct {
	MessageText string `json:"messageText"`
}

// liveChatMessageResource is the subset of YouTube's LiveChatMessage
// resource (https://developers.google.com/youtube/v3/live/docs/liveChatMessages#resource)
// this app reads back from a successful insert.
type liveChatMessageResource struct {
	ID      string `json:"id"`
	Snippet struct {
		PublishedAt       string `json:"publishedAt"`
		HasDisplayContent bool   `json:"hasDisplayContent"`
		DisplayMessage    string `json:"displayMessage"`
	} `json:"snippet"`
	AuthorDetails struct {
		DisplayName     string `json:"displayName"`
		ChannelID       string `json:"channelId"`
		ProfileImageURL string `json:"profileImageUrl"`
		IsChatModerator bool   `json:"isChatModerator"`
		IsChatSponsor   bool   `json:"isChatSponsor"`
		IsChatOwner     bool   `json:"isChatOwner"`
	} `json:"authorDetails"`
}

// sendYouTubeChatMessage posts a new text message to liveChatID, sent as
// the signed-in user, and returns the message YouTube created. The
// caller uses this to show the sent message immediately rather than
// waiting for it to come back through the chat stream: liveChatMessages
// sent via the API can take a while to surface there, or — for an
// account YouTube doesn't yet trust — get held for moderator review
// and never surface at all.
// https://developers.google.com/youtube/v3/live/docs/liveChatMessages/insert
func sendYouTubeChatMessage(ctx context.Context, accessToken, liveChatID, message string) (*youtubeChatMessageDTO, error) {
	body, err := json.Marshal(liveChatSendMessageRequest{Snippet: liveChatSendMessageSnippet{
		LiveChatID:         liveChatID,
		Type:               "textMessageEvent",
		TextMessageDetails: liveChatSendMessageTextDetail{MessageText: message},
	}})
	if err != nil {
		return nil, err
	}

	endpoint := youtubeAPIBase + "/liveChat/messages?" + url.Values{"part": {"snippet,authorDetails"}}.Encode()
	var resource liveChatMessageResource
	if err := youtubeAPIRequest(ctx, http.MethodPost, endpoint, accessToken, body, &resource); err != nil {
		return nil, err
	}

	ts := time.Now().UnixMilli()
	if parsed, err := time.Parse(time.RFC3339, resource.Snippet.PublishedAt); err == nil {
		ts = parsed.UnixMilli()
	}
	return &youtubeChatMessageDTO{
		ID:           resource.ID,
		Text:         resource.Snippet.DisplayMessage,
		DisplayName:  resource.AuthorDetails.DisplayName,
		ChannelID:    resource.AuthorDetails.ChannelID,
		ProfileImage: resource.AuthorDetails.ProfileImageURL,
		Mod:          resource.AuthorDetails.IsChatModerator,
		Subscriber:   resource.AuthorDetails.IsChatSponsor,
		Broadcaster:  resource.AuthorDetails.IsChatOwner,
		Timestamp:    ts,
	}, nil
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
