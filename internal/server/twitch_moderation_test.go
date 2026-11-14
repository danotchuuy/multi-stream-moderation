package server

import (
	"encoding/json"
	"testing"
)

// TestBanUserRequestShape guards against regressing to a `"data": [...]`
// array, which Twitch's Ban Users endpoint silently rejects as if
// user_id were missing (unlike most other Helix endpoints, "data" here
// is a single object).
func TestBanUserRequestShape(t *testing.T) {
	body := banUserRequest{Data: banUserEntry{
		UserID:   "12345",
		Duration: 60,
		Reason:   "spam",
	}}

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	dataField, ok := decoded["data"]
	if !ok {
		t.Fatal(`marshaled request has no "data" field`)
	}
	if len(dataField) == 0 || dataField[0] != '{' {
		t.Fatalf(`"data" must marshal as a JSON object, got %s`, dataField)
	}

	var entry banUserEntry
	if err := json.Unmarshal(dataField, &entry); err != nil {
		t.Fatalf("data does not decode as banUserEntry: %v", err)
	}
	if entry.UserID != "12345" {
		t.Errorf("UserID = %q, want %q", entry.UserID, "12345")
	}
}
