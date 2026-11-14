package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"sync"
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
// durationSeconds if it's greater than zero, and returns the ban's own
// ID — the only handle liveChatBans.delete accepts for undoing it.
// https://developers.google.com/youtube/v3/live/docs/liveChatBans/insert
func banOrTimeoutYouTubeUser(ctx context.Context, accessToken, liveChatID, channelID string, durationSeconds int) (banID string, err error) {
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
		return "", err
	}

	endpoint := youtubeAPIBase + "/liveChat/bans?" + url.Values{"part": {"snippet"}}.Encode()
	var out struct {
		ID string `json:"id"`
	}
	if err := youtubeAPIRequest(ctx, http.MethodPost, endpoint, accessToken, body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// unbanYouTubeUser lifts a ban or ends a timeout early, by the ID
// liveChatBans.insert returned for it.
// https://developers.google.com/youtube/v3/live/docs/liveChatBans/delete
func unbanYouTubeUser(ctx context.Context, accessToken, banID string) error {
	endpoint := youtubeAPIBase + "/liveChat/bans?" + url.Values{"id": {banID}}.Encode()
	return youtubeAPIRequest(ctx, http.MethodDelete, endpoint, accessToken, nil, nil)
}

type youtubeBanKey struct {
	liveChatID string
	channelID  string
}

// youtubeBanIDs remembers the ID of each ban or timeout made from this
// app, per live chat and banned channel. YouTube has no way to list a
// chat's bans or look one up by user, so a ban made anywhere else — or
// before this process started — can't be undone from here.
type youtubeBanIDs struct {
	mu  sync.Mutex
	ids map[youtubeBanKey]string
}

func newYouTubeBanIDs() *youtubeBanIDs {
	return &youtubeBanIDs{ids: make(map[youtubeBanKey]string)}
}

func (b *youtubeBanIDs) set(liveChatID, channelID, banID string) {
	if banID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ids[youtubeBanKey{liveChatID, channelID}] = banID
}

func (b *youtubeBanIDs) get(liveChatID, channelID string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id, ok := b.ids[youtubeBanKey{liveChatID, channelID}]
	return id, ok
}

func (b *youtubeBanIDs) remove(liveChatID, channelID, banID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := youtubeBanKey{liveChatID, channelID}
	if b.ids[key] == banID {
		delete(b.ids, key)
	}
}
