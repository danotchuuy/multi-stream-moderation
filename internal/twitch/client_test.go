package twitch

import (
	"context"
	"testing"
	"time"
)

func TestParseLinePrivmsg(t *testing.T) {
	line := `@badge-info=;badges=broadcaster/1;color=#FF0000;display-name=SomeUser;id=abc-123;mod=0;subscriber=0;tmi-sent-ts=1700000000000;user-id=456 :someuser!someuser@someuser.tmi.twitch.tv PRIVMSG #achannel :Hello world`

	p, err := parseLine(line)
	if err != nil {
		t.Fatalf("parseLine returned error: %v", err)
	}
	if p.Command != "PRIVMSG" {
		t.Errorf("Command = %q, want PRIVMSG", p.Command)
	}
	if len(p.Params) != 2 || p.Params[0] != "#achannel" || p.Params[1] != "Hello world" {
		t.Errorf("Params = %#v, want [#achannel, Hello world]", p.Params)
	}
	if p.Prefix != "someuser!someuser@someuser.tmi.twitch.tv" {
		t.Errorf("Prefix = %q", p.Prefix)
	}
	if p.Tags["display-name"] != "SomeUser" {
		t.Errorf("display-name tag = %q", p.Tags["display-name"])
	}

	msg := newMessage(p, line)
	if msg.Channel != "#achannel" || msg.Text != "Hello world" {
		t.Errorf("newMessage() = %+v", msg)
	}
	if msg.Username != "someuser" || msg.DisplayName != "SomeUser" {
		t.Errorf("newMessage() username/display = %q/%q", msg.Username, msg.DisplayName)
	}
	if !msg.Broadcaster {
		t.Error("expected Broadcaster to be true")
	}
	if msg.ID != "abc-123" || msg.UserID != "456" {
		t.Errorf("ID/UserID = %q/%q", msg.ID, msg.UserID)
	}
	wantTS := time.UnixMilli(1700000000000)
	if !msg.Timestamp.Equal(wantTS) {
		t.Errorf("Timestamp = %v, want %v", msg.Timestamp, wantTS)
	}
}

func TestParseLinePing(t *testing.T) {
	p, err := parseLine("PING :tmi.twitch.tv")
	if err != nil {
		t.Fatalf("parseLine returned error: %v", err)
	}
	if p.Command != "PING" {
		t.Errorf("Command = %q, want PING", p.Command)
	}
	if len(p.Params) != 1 || p.Params[0] != "tmi.twitch.tv" {
		t.Errorf("Params = %#v", p.Params)
	}
}

func TestParseLineNoTrailing(t *testing.T) {
	p, err := parseLine(":user!user@user.tmi.twitch.tv JOIN #achannel")
	if err != nil {
		t.Fatalf("parseLine returned error: %v", err)
	}
	if p.Command != "JOIN" {
		t.Errorf("Command = %q, want JOIN", p.Command)
	}
	if len(p.Params) != 1 || p.Params[0] != "#achannel" {
		t.Errorf("Params = %#v", p.Params)
	}
}

func TestUnescapeTagValue(t *testing.T) {
	cases := map[string]string{
		`hello\sworld`: "hello world",
		`a\:b`:         "a;b",
		`a\\b`:         `a\b`,
		`plain`:        "plain",
	}
	for in, want := range cases {
		if got := unescapeTagValue(in); got != want {
			t.Errorf("unescapeTagValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeChannel(t *testing.T) {
	cases := map[string]string{
		"Foo":  "#foo",
		"#Foo": "#foo",
		" foo": "#foo",
	}
	for in, want := range cases {
		if got := normalizeChannel(in); got != want {
			t.Errorf("normalizeChannel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRateLimiterAllowsBurstThenBlocks(t *testing.T) {
	rl := newRateLimiter(2, 100*time.Millisecond)
	ctx := context.Background()

	start := time.Now()
	if err := rl.Wait(ctx); err != nil {
		t.Fatalf("Wait 1: %v", err)
	}
	if err := rl.Wait(ctx); err != nil {
		t.Fatalf("Wait 2: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Millisecond {
		t.Errorf("first two waits took %v, want near-instant", elapsed)
	}

	if err := rl.Wait(ctx); err != nil {
		t.Fatalf("Wait 3: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 90*time.Millisecond {
		t.Errorf("third wait took %v, want to block until window elapses", elapsed)
	}
}

func TestRateLimiterRespectsContext(t *testing.T) {
	rl := newRateLimiter(1, time.Second)
	ctx := context.Background()
	if err := rl.Wait(ctx); err != nil {
		t.Fatalf("Wait 1: %v", err)
	}

	ctx2, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := rl.Wait(ctx2); err == nil {
		t.Error("expected Wait to return an error once ctx is done")
	}
}
