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

func TestWriteTwitchNotificationChatMessageSharedChatSource(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		wantLogin string
		wantName  string
	}{
		{
			name:      "from another channel in the session",
			source:    `"source_broadcaster_user_id": "999", "source_broadcaster_user_login": "otherchannel", "source_broadcaster_user_name": "OtherChannel",`,
			wantLogin: "otherchannel",
			wantName:  "OtherChannel",
		},
		{
			name:   "from this channel during a session",
			source: `"source_broadcaster_user_id": "123", "source_broadcaster_user_login": "achannel", "source_broadcaster_user_name": "AChannel",`,
		},
		{
			name:   "no shared chat session",
			source: `"source_broadcaster_user_id": null, "source_broadcaster_user_login": null, "source_broadcaster_user_name": null,`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := `{
				"broadcaster_user_id": "123",
				"message_id": "msg-1",
				"chatter_user_id": "456",
				"chatter_user_login": "someuser",
				"chatter_user_name": "SomeUser",
				` + tt.source + `
				"message": {"text": "hello"}
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
			if dto.SourceChannel != tt.wantLogin || dto.SourceChannelName != tt.wantName {
				t.Errorf("source = %q/%q, want %q/%q", dto.SourceChannel, dto.SourceChannelName, tt.wantLogin, tt.wantName)
			}
		})
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

func TestWriteTwitchNotificationChatMessageWithGifFragment(t *testing.T) {
	event := `{
		"message_id": "msg-1",
		"chatter_user_id": "456",
		"chatter_user_login": "someuser",
		"chatter_user_name": "SomeUser",
		"message": {
			"text": "check this out",
			"fragments": [
				{"type": "text", "text": "check this out "},
				{"type": "gif", "text": "", "gif": {"id": "joSNxeswxuc74Juo8X", "url": "https://cdn.7tv.app/gif/joSNxeswxuc74Juo8X"}}
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
	if len(dto.Fragments) != 2 {
		t.Fatalf("Fragments = %+v, want 2 fragments", dto.Fragments)
	}
	if dto.Fragments[1].ImageURL != "https://cdn.7tv.app/gif/joSNxeswxuc74Juo8X" || !dto.Fragments[1].Gif {
		t.Errorf("Fragments[1] = %+v, want a gif fragment with its URL and Gif set", dto.Fragments[1])
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

func TestWriteTwitchNotificationSub(t *testing.T) {
	event := `{
		"notice_type": "sub",
		"chatter_user_name": "NewSubber",
		"sub": {"sub_tier": "1000", "is_prime": false}
	}`

	rec := httptest.NewRecorder()
	writeTwitchNotification(rec, "achannel", twitch.Notification{Type: "channel.chat.notification", Event: json.RawMessage(event)}, nil)

	events := sseEvents(t, rec.Body.String())
	if len(events) != 1 || events[0].event != "sub" {
		t.Fatalf("events = %+v, want one \"sub\" event", events)
	}
	var dto subDTO
	if err := json.Unmarshal([]byte(events[0].data), &dto); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if dto.DisplayName != "NewSubber" || dto.Tier != "Tier 1" || dto.Months != 1 || dto.IsResub {
		t.Errorf("dto = %+v", dto)
	}
}

func TestWriteTwitchNotificationResub(t *testing.T) {
	event := `{
		"notice_type": "resub",
		"chatter_user_name": "LoyalSubber",
		"resub": {"cumulative_months": 14, "streak_months": 6, "sub_tier": "2000", "is_prime": false},
		"message": {"text": "Thanks for 14 great months!"}
	}`

	rec := httptest.NewRecorder()
	writeTwitchNotification(rec, "achannel", twitch.Notification{Type: "channel.chat.notification", Event: json.RawMessage(event)}, nil)

	events := sseEvents(t, rec.Body.String())
	if len(events) != 1 || events[0].event != "sub" {
		t.Fatalf("events = %+v, want one \"sub\" event", events)
	}
	var dto subDTO
	if err := json.Unmarshal([]byte(events[0].data), &dto); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if dto.DisplayName != "LoyalSubber" || dto.Tier != "Tier 2" || dto.Months != 14 || dto.StreakMonths != 6 || !dto.IsResub {
		t.Errorf("dto = %+v", dto)
	}
	if dto.Message != "Thanks for 14 great months!" {
		t.Errorf("dto.Message = %q, want the attached resub message", dto.Message)
	}
}

func TestWriteTwitchNotificationResubWithNoMessage(t *testing.T) {
	event := `{
		"notice_type": "resub",
		"chatter_user_name": "QuietSubber",
		"resub": {"cumulative_months": 3, "sub_tier": "1000", "is_prime": false}
	}`

	rec := httptest.NewRecorder()
	writeTwitchNotification(rec, "achannel", twitch.Notification{Type: "channel.chat.notification", Event: json.RawMessage(event)}, nil)

	events := sseEvents(t, rec.Body.String())
	var dto subDTO
	if err := json.Unmarshal([]byte(events[0].data), &dto); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if dto.Message != "" {
		t.Errorf("dto.Message = %q, want empty when no message was attached", dto.Message)
	}
}

func TestWriteTwitchNotificationPrimeSub(t *testing.T) {
	event := `{
		"notice_type": "sub",
		"chatter_user_name": "PrimeSubber",
		"sub": {"sub_tier": "1000", "is_prime": true}
	}`

	rec := httptest.NewRecorder()
	writeTwitchNotification(rec, "achannel", twitch.Notification{Type: "channel.chat.notification", Event: json.RawMessage(event)}, nil)

	events := sseEvents(t, rec.Body.String())
	var dto subDTO
	if err := json.Unmarshal([]byte(events[0].data), &dto); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if dto.Tier != "Prime" {
		t.Errorf("dto.Tier = %q, want Prime", dto.Tier)
	}
}

func TestWriteTwitchNotificationCheer(t *testing.T) {
	event := `{
		"is_anonymous": false,
		"user_id": "123",
		"user_login": "cheerer",
		"user_name": "Cheerer",
		"message": "Take my bits! cheer100",
		"bits": 100
	}`

	rec := httptest.NewRecorder()
	writeTwitchNotification(rec, "achannel", twitch.Notification{Type: "channel.cheer", Event: json.RawMessage(event)}, nil)

	events := sseEvents(t, rec.Body.String())
	if len(events) != 1 || events[0].event != "cheer" {
		t.Fatalf("events = %+v, want one \"cheer\" event", events)
	}
	var dto cheerDTO
	if err := json.Unmarshal([]byte(events[0].data), &dto); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if dto.DisplayName != "Cheerer" || dto.Bits != 100 || dto.Message != "Take my bits! cheer100" {
		t.Errorf("dto = %+v", dto)
	}
}

func TestWriteTwitchNotificationAnonymousCheer(t *testing.T) {
	event := `{
		"is_anonymous": true,
		"bits": 50
	}`

	rec := httptest.NewRecorder()
	writeTwitchNotification(rec, "achannel", twitch.Notification{Type: "channel.cheer", Event: json.RawMessage(event)}, nil)

	events := sseEvents(t, rec.Body.String())
	if len(events) != 1 || events[0].event != "cheer" {
		t.Fatalf("events = %+v, want one \"cheer\" event", events)
	}
	var dto cheerDTO
	if err := json.Unmarshal([]byte(events[0].data), &dto); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if dto.DisplayName != "An anonymous cheerer" || dto.Bits != 50 {
		t.Errorf("dto = %+v, want an anonymous placeholder and Bits 50", dto)
	}
}

func TestWriteTwitchNotificationAutomodMessageHold(t *testing.T) {
	event := `{
		"message_id": "held-1",
		"user_id": "456",
		"user_login": "someuser",
		"user_name": "SomeUser",
		"message": {
			"text": "hello Kappa world",
			"fragments": [
				{"type": "text", "text": "hello "},
				{"type": "emote", "text": "Kappa", "emote": {"id": "25"}},
				{"type": "text", "text": " world"}
			]
		},
		"reason": "automod",
		"automod": {"category": "profanity", "level": 2},
		"held_at": "2026-01-01T00:00:00Z"
	}`

	rec := httptest.NewRecorder()
	writeTwitchNotification(rec, "achannel", twitch.Notification{Type: "automod.message.hold", Event: json.RawMessage(event)}, nil)

	events := sseEvents(t, rec.Body.String())
	if len(events) != 1 || events[0].event != "automodhold" {
		t.Fatalf("events = %+v, want one \"automodhold\" event", events)
	}
	var dto automodHoldDTO
	if err := json.Unmarshal([]byte(events[0].data), &dto); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if dto.Channel != "achannel" || dto.MessageID != "held-1" {
		t.Errorf("dto = %+v", dto)
	}
	if dto.Username != "someuser" || dto.DisplayName != "SomeUser" || dto.UserID != "456" {
		t.Errorf("dto user fields = %+v", dto)
	}
	if dto.Reason != "automod" || dto.Category != "profanity" || dto.Level != 2 {
		t.Errorf("dto reason fields = %+v", dto)
	}
	if len(dto.Fragments) != 3 || dto.Fragments[1].ImageURL == "" {
		t.Errorf("dto.Fragments = %+v, want the emote fragment resolved", dto.Fragments)
	}
}

func TestWriteTwitchNotificationAutomodMessageUpdate(t *testing.T) {
	event := `{"message_id": "held-1", "status": "Approved", "moderator_user_name": "SomeMod"}`

	rec := httptest.NewRecorder()
	writeTwitchNotification(rec, "achannel", twitch.Notification{Type: "automod.message.update", Event: json.RawMessage(event)}, nil)

	events := sseEvents(t, rec.Body.String())
	if len(events) != 1 || events[0].event != "automodupdate" {
		t.Fatalf("events = %+v, want one \"automodupdate\" event", events)
	}
	var dto automodUpdateDTO
	if err := json.Unmarshal([]byte(events[0].data), &dto); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if dto.Channel != "achannel" || dto.MessageID != "held-1" || dto.Status != "Approved" {
		t.Errorf("dto = %+v", dto)
	}
}

func TestWriteTwitchNotificationUnhandledNoticeIgnored(t *testing.T) {
	event := `{"notice_type": "announcement"}`

	rec := httptest.NewRecorder()
	writeTwitchNotification(rec, "achannel", twitch.Notification{Type: "channel.chat.notification", Event: json.RawMessage(event)}, nil)

	if rec.Body.Len() != 0 {
		t.Errorf("expected no SSE event for an unhandled notice type, got %q", rec.Body.String())
	}
}
