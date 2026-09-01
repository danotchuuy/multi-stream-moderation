package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
)

// twitchChatEventSubTypes lists the five EventSub subscription types
// that together replace what a single Twitch IRC connection used to
// deliver: chat messages, single-message deletion, full-chat clears,
// per-user clears (bans/timeouts), and chat-room notices (raids, subs,
// ...). All five need only the user:read:chat scope and — like IRC's
// anonymous read access — work for any channel the signed-in user wants
// to watch, not just ones they moderate.
// https://dev.twitch.tv/docs/eventsub/eventsub-subscription-types/
var twitchChatEventSubTypes = []string{
	"channel.chat.message",
	"channel.chat.message_delete",
	"channel.chat.clear",
	"channel.chat.clear_user_messages",
	"channel.chat.notification",
}

type createEventSubSubscriptionRequest struct {
	Type      string            `json:"type"`
	Version   string            `json:"version"`
	Condition map[string]string `json:"condition"`
	Transport struct {
		Method    string `json:"method"`
		SessionID string `json:"session_id"`
	} `json:"transport"`
}

// createChatEventSubSubscriptions subscribes sessionID (an already-open
// EventSub WebSocket session, see internal/twitch.Connect) to every
// event type in twitchChatEventSubTypes for broadcasterID, read as
// userID (the signed-in viewer). The five subscriptions are independent
// Helix calls, so they're created concurrently rather than one at a
// time — otherwise their round trips simply add up, and Twitch requires
// the session's first subscription within 10 seconds of it opening. It
// returns whichever subscription IDs were successfully created, even if
// others failed, so the caller can still clean those up.
func createChatEventSubSubscriptions(ctx context.Context, clientID, accessToken, broadcasterID, userID, sessionID string) ([]string, error) {
	condition := map[string]string{"broadcaster_user_id": broadcasterID, "user_id": userID}

	type result struct {
		id  string
		err error
	}
	results := make([]result, len(twitchChatEventSubTypes))

	var wg sync.WaitGroup
	for i, subType := range twitchChatEventSubTypes {
		wg.Add(1)
		go func(i int, subType string) {
			defer wg.Done()
			id, err := createEventSubSubscription(ctx, clientID, accessToken, subType, "1", condition, sessionID)
			results[i] = result{id: id, err: err}
		}(i, subType)
	}
	wg.Wait()

	ids := make([]string, 0, len(results))
	var firstErr error
	for i, r := range results {
		if r.err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("subscribing to %s: %w", twitchChatEventSubTypes[i], r.err)
			}
			continue
		}
		ids = append(ids, r.id)
	}
	return ids, firstErr
}

func createEventSubSubscription(ctx context.Context, clientID, accessToken, subType, version string, condition map[string]string, sessionID string) (string, error) {
	req := createEventSubSubscriptionRequest{Type: subType, Version: version, Condition: condition}
	req.Transport.Method = "websocket"
	req.Transport.SessionID = sessionID

	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := twitchAPIRequest(ctx, http.MethodPost, twitchAPIBase+"/eventsub/subscriptions", clientID, accessToken, body, &out); err != nil {
		return "", err
	}
	if len(out.Data) == 0 {
		return "", fmt.Errorf("twitch: no subscription id returned for %s", subType)
	}
	return out.Data[0].ID, nil
}

// deleteEventSubSubscription removes a subscription created above.
// Twitch would eventually clean these up on its own once the WebSocket
// they were delivering to closes, but there's no reason to wait for
// that once a chat stream ends.
func deleteEventSubSubscription(ctx context.Context, clientID, accessToken, subscriptionID string) error {
	endpoint := twitchAPIBase + "/eventsub/subscriptions?" + url.Values{"id": {subscriptionID}}.Encode()
	return twitchAPIRequest(ctx, http.MethodDelete, endpoint, clientID, accessToken, nil, nil)
}
