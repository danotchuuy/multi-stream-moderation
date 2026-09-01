package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestKickMessageFragmentsNoEmotes(t *testing.T) {
	if frags := kickMessageFragments("just plain text"); frags != nil {
		t.Errorf("kickMessageFragments = %+v, want nil for a message with no emote tokens", frags)
	}
}

func TestKickMessageFragmentsSingleEmote(t *testing.T) {
	frags := kickMessageFragments("[emote:39262:Prayge]")
	if len(frags) != 1 {
		t.Fatalf("frags = %+v, want 1 fragment", frags)
	}
	if frags[0].Text != "Prayge" || frags[0].ImageURL == "" {
		t.Errorf("frags[0] = %+v, want an emote fragment named Prayge", frags[0])
	}
}

func TestKickMessageFragmentsMixedTextAndEmotes(t *testing.T) {
	frags := kickMessageFragments("hello [emote:39262:Prayge] world [emote:5:KEKW]!")

	want := []struct {
		text    string
		isEmote bool
	}{
		{"hello ", false},
		{"Prayge", true},
		{" world ", false},
		{"KEKW", true},
		{"!", false},
	}
	if len(frags) != len(want) {
		t.Fatalf("frags = %+v, want %d fragments", frags, len(want))
	}
	for i, w := range want {
		if frags[i].Text != w.text {
			t.Errorf("frags[%d].Text = %q, want %q", i, frags[i].Text, w.text)
		}
		if hasImage := frags[i].ImageURL != ""; hasImage != w.isEmote {
			t.Errorf("frags[%d].ImageURL set = %v, want %v", i, hasImage, w.isEmote)
		}
	}
}

func TestKickMessageFragmentsEmoteAtStartAndEnd(t *testing.T) {
	frags := kickMessageFragments("[emote:1:A][emote:2:B]")
	if len(frags) != 2 {
		t.Fatalf("frags = %+v, want 2 fragments (no empty text fragments between adjacent emotes)", frags)
	}
	if frags[0].Text != "A" || frags[1].Text != "B" {
		t.Errorf("frags = %+v, want [A B]", frags)
	}
}

func TestWriteKickStreamEventGiftSub(t *testing.T) {
	ev := kickStreamEvent{
		kind: "giftsub",
		giftSubs: kickSubscriptionGiftsEvent{
			Gifter: struct {
				IsAnonymous bool   `json:"is_anonymous"`
				Username    string `json:"username"`
			}{Username: "Gifty"},
			Giftees: []struct {
				UserID int64 `json:"user_id"`
			}{{UserID: 1}, {UserID: 2}, {UserID: 3}},
		},
	}

	rec := httptest.NewRecorder()
	writeKickStreamEvent(rec, "achannel", ev)

	events := sseEvents(t, rec.Body.String())
	if len(events) != 1 || events[0].event != "giftsub" {
		t.Fatalf("events = %+v, want one \"giftsub\" event", events)
	}
	var dto giftSubDTO
	if err := json.Unmarshal([]byte(events[0].data), &dto); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if dto.DisplayName != "Gifty" || dto.Count != 3 {
		t.Errorf("dto = %+v, want DisplayName Gifty, Count 3", dto)
	}
}

func TestWriteKickStreamEventGiftSubAnonymous(t *testing.T) {
	ev := kickStreamEvent{
		kind: "giftsub",
		giftSubs: kickSubscriptionGiftsEvent{
			Gifter: struct {
				IsAnonymous bool   `json:"is_anonymous"`
				Username    string `json:"username"`
			}{IsAnonymous: true},
		},
	}

	rec := httptest.NewRecorder()
	writeKickStreamEvent(rec, "achannel", ev)

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
