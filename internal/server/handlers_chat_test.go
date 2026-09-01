package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danotchuuy/multi-stream-moderation/internal/twitch"
)

// sseEvents splits a recorded SSE body into (event name, data) pairs, in
// order, for asserting on writeTwitchNotification's output.
func sseEvents(t *testing.T, body string) []struct{ event, data string } {
	t.Helper()
	var out []struct{ event, data string }
	for _, block := range strings.Split(strings.TrimSpace(body), "\n\n") {
		if block == "" {
			continue
		}
		var ev, data string
		for _, line := range strings.Split(block, "\n") {
			if rest, ok := strings.CutPrefix(line, "event: "); ok {
				ev = rest
			} else if rest, ok := strings.CutPrefix(line, "data: "); ok {
				data = rest
			}
		}
		out = append(out, struct{ event, data string }{ev, data})
	}
	return out
}

func TestWriteTwitchNotificationChatMessage(t *testing.T) {
	event := `{
		"message_id": "msg-1",
		"chatter_user_id": "456",
		"chatter_user_login": "someuser",
		"chatter_user_name": "SomeUser",
		"message": {"text": "hello"},
		"message_type": "user_intro",
		"badges": [{"set_id": "moderator", "id": "1"}],
		"color": "#123456",
		"reply": {
			"parent_message_id": "parent-1",
			"parent_user_id": "789",
			"parent_user_login": "other",
			"parent_user_name": "Other",
			"parent_message_body": "hi there"
		}
	}`

	rec := httptest.NewRecorder()
	writeTwitchNotification(rec, "achannel", twitch.Notification{Type: "channel.chat.message", Event: json.RawMessage(event)}, nil)

	events := sseEvents(t, rec.Body.String())
	if len(events) != 1 || events[0].event != "message" {
		t.Fatalf("events = %+v, want one \"message\" event", events)
	}

	var dto chatMessageDTO
	if err := json.Unmarshal([]byte(events[0].data), &dto); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if dto.ID != "msg-1" || dto.Channel != "achannel" || dto.Text != "hello" {
		t.Errorf("dto = %+v", dto)
	}
	if dto.Username != "someuser" || dto.DisplayName != "SomeUser" || dto.UserID != "456" {
		t.Errorf("dto user fields = %+v", dto)
	}
	if !dto.Mod {
		t.Error("expected Mod to be true from the moderator badge")
	}
	if !dto.FirstMessage {
		t.Error("expected FirstMessage to be true for message_type user_intro")
	}
	if dto.Reply == nil || dto.Reply.Username != "other" || dto.Reply.Text != "hi there" {
		t.Errorf("dto.Reply = %+v", dto.Reply)
	}
}

func TestWriteTwitchNotificationChatMessageResolvesBadgeImages(t *testing.T) {
	event := `{
		"message_id": "msg-1",
		"chatter_user_id": "456",
		"chatter_user_login": "someuser",
		"chatter_user_name": "SomeUser",
		"message": {"text": "hello"},
		"badges": [
			{"set_id": "moderator", "id": "1"},
			{"set_id": "subscriber", "id": "3"}
		]
	}`
	catalog := twitchBadgeCatalog{
		"moderator": {"1": "https://static-cdn.jtvnw.net/badges/v1/moderator/3"},
		// No entry for subscriber/3 — an unrecognized or since-removed
		// badge/version should resolve to "" rather than panic.
	}

	rec := httptest.NewRecorder()
	writeTwitchNotification(rec, "achannel", twitch.Notification{Type: "channel.chat.message", Event: json.RawMessage(event)}, catalog)

	events := sseEvents(t, rec.Body.String())
	if len(events) != 1 || events[0].event != "message" {
		t.Fatalf("events = %+v, want one \"message\" event", events)
	}
	var dto chatMessageDTO
	if err := json.Unmarshal([]byte(events[0].data), &dto); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(dto.Badges) != 2 {
		t.Fatalf("Badges = %+v, want 2 badges", dto.Badges)
	}
	if dto.Badges[0].ImageURL != "https://static-cdn.jtvnw.net/badges/v1/moderator/3" {
		t.Errorf("Badges[0].ImageURL = %q, want the catalog's moderator image", dto.Badges[0].ImageURL)
	}
	if dto.Badges[1].ImageURL != "" {
		t.Errorf("Badges[1].ImageURL = %q, want empty for a badge/version missing from the catalog", dto.Badges[1].ImageURL)
	}
}

func TestWriteTwitchNotificationChatMessageWithEmoteFragments(t *testing.T) {
	event := `{
		"message_id": "msg-1",
		"chatter_user_id": "456",
		"chatter_user_login": "someuser",
		"chatter_user_name": "SomeUser",
		"message": {
			"text": "hello Kappa world",
			"fragments": [
				{"type": "text", "text": "hello "},
				{"type": "emote", "text": "Kappa", "emote": {"id": "25", "emote_set_id": "0", "owner_id": "0", "format": ["static"]}},
				{"type": "text", "text": " world"}
			]
		}
	}`

	rec := httptest.NewRecorder()
	writeTwitchNotification(rec, "achannel", twitch.Notification{Type: "channel.chat.message", Event: json.RawMessage(event)}, nil)

	events := sseEvents(t, rec.Body.String())
	if len(events) != 1 || events[0].event != "message" {
		t.Fatalf("events = %+v, want one \"message\" event", events)
	}

	var dto chatMessageDTO
	if err := json.Unmarshal([]byte(events[0].data), &dto); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(dto.Fragments) != 3 {
		t.Fatalf("Fragments = %+v, want 3 fragments", dto.Fragments)
	}
	if dto.Fragments[0].Text != "hello " || dto.Fragments[0].ImageURL != "" {
		t.Errorf("Fragments[0] = %+v, want a plain text fragment", dto.Fragments[0])
	}
	if dto.Fragments[1].Text != "Kappa" || dto.Fragments[1].ImageURL == "" {
		t.Errorf("Fragments[1] = %+v, want an emote fragment with an image URL", dto.Fragments[1])
	}
	if dto.Fragments[2].Text != " world" || dto.Fragments[2].ImageURL != "" {
		t.Errorf("Fragments[2] = %+v, want a plain text fragment", dto.Fragments[2])
	}
}

func TestWriteTwitchNotificationMessageDelete(t *testing.T) {
	event := `{"message_id": "msg-1", "target_user_id": "456", "target_user_login": "someuser"}`

	rec := httptest.NewRecorder()
	writeTwitchNotification(rec, "achannel", twitch.Notification{Type: "channel.chat.message_delete", Event: json.RawMessage(event)}, nil)

	events := sseEvents(t, rec.Body.String())
	if len(events) != 1 || events[0].event != "clearmsg" {
		t.Fatalf("events = %+v, want one \"clearmsg\" event", events)
	}
	var dto clearMsgDTO
	if err := json.Unmarshal([]byte(events[0].data), &dto); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if dto.Channel != "achannel" || dto.MessageID != "msg-1" {
		t.Errorf("dto = %+v", dto)
	}
}

func TestWriteTwitchNotificationClear(t *testing.T) {
	rec := httptest.NewRecorder()
	writeTwitchNotification(rec, "achannel", twitch.Notification{Type: "channel.chat.clear", Event: json.RawMessage(`{}`)}, nil)

	events := sseEvents(t, rec.Body.String())
	if len(events) != 1 || events[0].event != "clearchat" {
		t.Fatalf("events = %+v, want one \"clearchat\" event", events)
	}
	var dto clearChatDTO
	if err := json.Unmarshal([]byte(events[0].data), &dto); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if dto.UserID != "" {
		t.Errorf("expected no UserID on a full clear, got %+v", dto)
	}
}

func TestWriteTwitchNotificationClearUserMessages(t *testing.T) {
	event := `{"target_user_id": "456", "target_user_login": "someuser"}`

	rec := httptest.NewRecorder()
	writeTwitchNotification(rec, "achannel", twitch.Notification{Type: "channel.chat.clear_user_messages", Event: json.RawMessage(event)}, nil)

	events := sseEvents(t, rec.Body.String())
	if len(events) != 1 || events[0].event != "clearchat" {
		t.Fatalf("events = %+v, want one \"clearchat\" event", events)
	}
	var dto clearChatDTO
	if err := json.Unmarshal([]byte(events[0].data), &dto); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if dto.UserID != "456" || dto.Username != "someuser" {
		t.Errorf("dto = %+v", dto)
	}
}

func TestWriteTwitchNotificationRaid(t *testing.T) {
	event := `{
		"notice_type": "raid",
		"raid": {"user_id": "111", "user_login": "raider", "user_name": "Raider", "viewer_count": 42}
	}`

	rec := httptest.NewRecorder()
	writeTwitchNotification(rec, "achannel", twitch.Notification{Type: "channel.chat.notification", Event: json.RawMessage(event)}, nil)

	events := sseEvents(t, rec.Body.String())
	if len(events) != 1 || events[0].event != "raid" {
		t.Fatalf("events = %+v, want one \"raid\" event", events)
	}
	var dto raidDTO
	if err := json.Unmarshal([]byte(events[0].data), &dto); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if dto.Login != "raider" || dto.DisplayName != "Raider" || dto.ViewerCount != 42 {
		t.Errorf("dto = %+v", dto)
	}
}

func TestWriteTwitchNotificationCommunitySubGift(t *testing.T) {
	event := `{
		"notice_type": "community_sub_gift",
		"chatter_user_name": "Gifty",
		"community_sub_gift": {"id": "gift-1", "total": 5, "sub_tier": "1000"}
	}`

	rec := httptest.NewRecorder()
	writeTwitchNotification(rec, "achannel", twitch.Notification{Type: "channel.chat.notification", Event: json.RawMessage(event)}, nil)

	events := sseEvents(t, rec.Body.String())
	if len(events) != 1 || events[0].event != "giftsub" {
		t.Fatalf("events = %+v, want one \"giftsub\" event", events)
	}
	var dto giftSubDTO
	if err := json.Unmarshal([]byte(events[0].data), &dto); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if dto.DisplayName != "Gifty" || dto.Count != 5 {
		t.Errorf("dto = %+v, want DisplayName Gifty, Count 5", dto)
	}
}

func TestWriteTwitchNotificationStandaloneSubGift(t *testing.T) {
	event := `{
		"notice_type": "sub_gift",
		"chatter_user_name": "Gifty",
		"sub_gift": {"duration_months": 1, "recipient_user_id": "789", "sub_tier": "1000"}
	}`

	rec := httptest.NewRecorder()
	writeTwitchNotification(rec, "achannel", twitch.Notification{Type: "channel.chat.notification", Event: json.RawMessage(event)}, nil)

	events := sseEvents(t, rec.Body.String())
	if len(events) != 1 || events[0].event != "giftsub" {
		t.Fatalf("events = %+v, want one \"giftsub\" event", events)
	}
	var dto giftSubDTO
	if err := json.Unmarshal([]byte(events[0].data), &dto); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if dto.DisplayName != "Gifty" || dto.Count != 1 {
		t.Errorf("dto = %+v, want DisplayName Gifty, Count 1", dto)
	}
}

func TestWriteTwitchNotificationSubGiftInCommunityGiftIsSkipped(t *testing.T) {
	// One of these arrives per recipient inside a mass gift — reporting
	// it too would double-count on top of the community_sub_gift event
	// that already reports the whole batch.
	event := `{
		"notice_type": "sub_gift",
		"chatter_user_name": "Gifty",
		"sub_gift": {"duration_months": 1, "recipient_user_id": "789", "sub_tier": "1000", "community_gift_id": "gift-1"}
	}`

	rec := httptest.NewRecorder()
	writeTwitchNotification(rec, "achannel", twitch.Notification{Type: "channel.chat.notification", Event: json.RawMessage(event)}, nil)

	if rec.Body.Len() != 0 {
		t.Errorf("expected no SSE event for a sub_gift that's part of a community gift, got %q", rec.Body.String())
	}
}

func TestWriteTwitchNotificationAnonymousGiftSub(t *testing.T) {
	event := `{
		"notice_type": "community_sub_gift",
		"chatter_is_anonymous": true,
		"community_sub_gift": {"id": "gift-1", "total": 3, "sub_tier": "1000"}
	}`

	rec := httptest.NewRecorder()
	writeTwitchNotification(rec, "achannel", twitch.Notification{Type: "channel.chat.notification", Event: json.RawMessage(event)}, nil)

	events := sseEvents(t, rec.Body.String())
	if len(events) != 1 || events[0].event != "giftsub" {
		t.Fatalf("events = %+v, want one \"giftsub\" event", events)
	}
	var dto giftSubDTO
	if err := json.Unmarshal([]byte(events[0].data), &dto); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if dto.DisplayName != "An anonymous gifter" {
		t.Errorf("dto.DisplayName = %q, want an anonymous placeholder", dto.DisplayName)
	}
}

func TestWriteTwitchNotificationNonRaidNoticeIgnored(t *testing.T) {
	event := `{"notice_type": "sub"}`

	rec := httptest.NewRecorder()
	writeTwitchNotification(rec, "achannel", twitch.Notification{Type: "channel.chat.notification", Event: json.RawMessage(event)}, nil)

	if rec.Body.Len() != 0 {
		t.Errorf("expected no SSE event for a non-raid notice, got %q", rec.Body.String())
	}
}
