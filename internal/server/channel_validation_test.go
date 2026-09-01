package server

import "testing"

func TestNormalizeTwitchChannel(t *testing.T) {
	valid := map[string]string{
		"someuser":                "someuser",
		"SomeUser":                "someuser",
		"  someuser  ":            "someuser",
		"#someuser":               "someuser",
		"#SomeUser":               "someuser",
		"a_b_c_1234":              "a_b_c_1234",
		"abcd":                    "abcd",                    // exactly the 4-char minimum
		"a2345678901234567890123": "a2345678901234567890123", // exactly 25
	}
	for in, want := range valid {
		got, err := normalizeTwitchChannel(in)
		if err != nil {
			t.Errorf("normalizeTwitchChannel(%q) returned error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("normalizeTwitchChannel(%q) = %q, want %q", in, got, want)
		}
	}

	invalid := []string{
		"",
		"   ",
		"abc",                         // 3 chars, below minimum
		"a23456789012345678901234567", // 27 chars, above maximum
		"has space",
		"has-dash",
		"has.dot",
		"emoji😀name",
		"https://twitch.tv/someuser",
	}
	for _, in := range invalid {
		if _, err := normalizeTwitchChannel(in); err == nil {
			t.Errorf("normalizeTwitchChannel(%q) should have returned an error", in)
		}
	}
}

func TestNormalizeKickChannel(t *testing.T) {
	valid := map[string]string{
		"someuser":                  "someuser",
		"SomeUser":                  "someuser",
		"  someuser  ":              "someuser",
		"#someuser":                 "someuser",
		"some-user_123":             "some-user_123",
		"a":                         "a",                         // 1 char minimum
		"a234567890123456789012345": "a234567890123456789012345", // exactly 25
	}
	for in, want := range valid {
		got, err := normalizeKickChannel(in)
		if err != nil {
			t.Errorf("normalizeKickChannel(%q) returned error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("normalizeKickChannel(%q) = %q, want %q", in, got, want)
		}
	}

	invalid := []string{
		"",
		"   ",
		"a2345678901234567890123456", // 26 chars, above maximum
		"has space",
		"has.dot",
		"emoji😀name",
		"https://kick.com/someuser",
	}
	for _, in := range invalid {
		if _, err := normalizeKickChannel(in); err == nil {
			t.Errorf("normalizeKickChannel(%q) should have returned an error", in)
		}
	}
}
