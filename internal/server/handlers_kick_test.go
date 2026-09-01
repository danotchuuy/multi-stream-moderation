package server

import "testing"

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
