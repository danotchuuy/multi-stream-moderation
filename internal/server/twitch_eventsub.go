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

// twitchAutomodEventSubTypes are the AutoMod queue subscription types —
// kept separate from twitchChatEventSubTypes above because they need a
// different condition shape (moderator_user_id instead of user_id) and
// a different version, and only succeed when the signed-in viewer is
// actually a moderator (or the broadcaster) of the channel; Twitch
// itself enforces that when the subscription is created, so there's
// nothing for this app to check beforehand.
// https://dev.twitch.tv/docs/eventsub/eventsub-subscription-types/
var twitchAutomodEventSubTypes = []string{
	"automod.message.hold",
	"automod.message.update",
}

const twitchAutomodEventSubVersion = "2"

// twitchCheerEventSubTypes is the Bits/Cheer subscription type — kept
// separate from twitchChatEventSubTypes because its condition only
// takes broadcaster_user_id (no user_id), and it needs the bits:read
// scope instead of user:read:chat.
// https://dev.twitch.tv/docs/eventsub/eventsub-subscription-types/#cheer
var twitchCheerEventSubTypes = []string{
	"channel.cheer",
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
// userID (the signed-in viewer). All five need only the user:read:chat
// scope and work for any channel the signed-in user wants to watch, not
// just ones they moderate.
func createChatEventSubSubscriptions(ctx context.Context, clientID, accessToken, broadcasterID, userID, sessionID string) ([]string, error) {
	condition := map[string]string{"broadcaster_user_id": broadcasterID, "user_id": userID}
	return createEventSubSubscriptions(ctx, clientID, accessToken, sessionID, "1", condition, twitchChatEventSubTypes)
}

// createAutomodEventSubSubscriptions subscribes sessionID to the
// AutoMod queue events (twitchAutomodEventSubTypes) for broadcasterID,
// moderated as moderatorUserID (the signed-in viewer). Expected to fail
// for anyone who isn't actually a moderator or the broadcaster — Twitch
// requires moderator:manage:automod for the specified broadcaster — so
// callers should treat a failure here as "no AutoMod queue for this
// viewer", not a reason to give up on chat entirely.
func createAutomodEventSubSubscriptions(ctx context.Context, clientID, accessToken, broadcasterID, moderatorUserID, sessionID string) ([]string, error) {
	condition := map[string]string{"broadcaster_user_id": broadcasterID, "moderator_user_id": moderatorUserID}
	return createEventSubSubscriptions(ctx, clientID, accessToken, sessionID, twitchAutomodEventSubVersion, condition, twitchAutomodEventSubTypes)
}

// createCheerEventSubSubscriptions subscribes sessionID to Bits cheer
// notifications (twitchCheerEventSubTypes) for broadcasterID. Expected
// to fail for anyone who isn't actually the broadcaster or a moderator
// there — see the bits:read scope's comment in twitch_oauth.go — so
// callers should treat a failure here as "no cheer callouts for this
// viewer", not a reason to give up on chat entirely.
func createCheerEventSubSubscriptions(ctx context.Context, clientID, accessToken, broadcasterID, sessionID string) ([]string, error) {
	condition := map[string]string{"broadcaster_user_id": broadcasterID}
	return createEventSubSubscriptions(ctx, clientID, accessToken, sessionID, "1", condition, twitchCheerEventSubTypes)
}

// createEventSubSubscriptions creates sessionID's subscriptions for
// every type in subTypes, all sharing condition and version. The
// requests run concurrently rather than one at a time — otherwise their
// round trips simply add up, and Twitch requires the session's first
// subscription within 10 seconds of it opening. Returns whichever
// subscription IDs were successfully created, even if others failed, so
// the caller can still clean those up.
func createEventSubSubscriptions(ctx context.Context, clientID, accessToken, sessionID, version string, condition map[string]string, subTypes []string) ([]string, error) {
	type result struct {
		id  string
		err error
	}
	results := make([]result, len(subTypes))

	var wg sync.WaitGroup
	for i, subType := range subTypes {
		wg.Add(1)
		go func(i int, subType string) {
			defer wg.Done()
			id, err := createEventSubSubscription(ctx, clientID, accessToken, subType, version, condition, sessionID)
			results[i] = result{id: id, err: err}
		}(i, subType)
	}
	wg.Wait()

	ids := make([]string, 0, len(results))
	var firstErr error
	for i, r := range results {
		if r.err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("subscribing to %s: %w", subTypes[i], r.err)
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
