package server

import (
	"encoding/json"
	"testing"
)

// TestLiveChatBanRequestShape guards against banDurationSeconds ever
// being sent as a JSON number: YouTube's schema types it as a string (a
// uint64 represented as a string to avoid JS precision loss), and a
// number there is silently ignored rather than rejected outright — the
// same class of shape mismatch that broke Kick's ban endpoint.
func TestLiveChatBanRequestShapeTemporary(t *testing.T) {
	body := liveChatBanRequest{Snippet: liveChatBanSnippet{
		LiveChatID:         "chat123",
		Type:               "temporary",
		BannedUserDetails:  liveChatBannedUserDetail{ChannelID: "UC123"},
		BanDurationSeconds: "600",
	}}

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	snippet, ok := decoded["snippet"]
	if !ok {
		t.Fatal(`marshaled request has no "snippet" field`)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(snippet, &fields); err != nil {
		t.Fatalf("Unmarshal snippet: %v", err)
	}

	dur, ok := fields["banDurationSeconds"]
	if !ok {
		t.Fatal(`snippet has no "banDurationSeconds" field`)
	}
	if len(dur) == 0 || dur[0] != '"' {
		t.Fatalf(`banDurationSeconds must marshal as a JSON string, got %s`, dur)
	}
	var s string
	if err := json.Unmarshal(dur, &s); err != nil || s != "600" {
		t.Fatalf("banDurationSeconds = %s, want the string \"600\"", dur)
	}
}

func TestLiveChatBanRequestShapePermanentOmitsDuration(t *testing.T) {
	body := liveChatBanRequest{Snippet: liveChatBanSnippet{
		LiveChatID:        "chat123",
		Type:              "permanent",
		BannedUserDetails: liveChatBannedUserDetail{ChannelID: "UC123"},
	}}

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(decoded["snippet"], &fields); err != nil {
		t.Fatalf("Unmarshal snippet: %v", err)
	}
	if _, ok := fields["banDurationSeconds"]; ok {
		t.Error(`a permanent ban should omit "banDurationSeconds", but it was present`)
	}
}

func TestParseYouTubeVideoID(t *testing.T) {
	cases := map[string]string{
		"dQw4w9WgXcQ": "dQw4w9WgXcQ",
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ":        "dQw4w9WgXcQ",
		"https://youtube.com/watch?v=dQw4w9WgXcQ&t=30s":      "dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ":                       "dQw4w9WgXcQ",
		"https://www.youtube.com/live/dQw4w9WgXcQ":           "dQw4w9WgXcQ",
		"https://www.youtube.com/live/dQw4w9WgXcQ?feature=x": "dQw4w9WgXcQ",
		"  dQw4w9WgXcQ  ":                                    "dQw4w9WgXcQ",
	}
	for input, want := range cases {
		got, err := parseYouTubeVideoID(input)
		if err != nil {
			t.Errorf("parseYouTubeVideoID(%q) returned error: %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("parseYouTubeVideoID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseYouTubeVideoIDRejectsGarbage(t *testing.T) {
	for _, input := range []string{"", "not a url or id", "https://example.com/watch?v=dQw4w9WgXcQ"} {
		if _, err := parseYouTubeVideoID(input); err == nil {
			t.Errorf("parseYouTubeVideoID(%q) should have returned an error", input)
		}
	}
}
