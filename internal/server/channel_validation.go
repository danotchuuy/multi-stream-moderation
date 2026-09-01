package server

import (
	"fmt"
	"regexp"
	"strings"
)

// twitchLoginPattern matches Twitch's own username rules: 4-25
// characters, letters/digits/underscores only.
var twitchLoginPattern = regexp.MustCompile(`^[a-zA-Z0-9_]{4,25}$`)

// kickSlugPattern matches Kick channel slugs: up to 25 characters (the
// limit Kick's own API documents for the slug query parameter),
// letters/digits/underscores/hyphens — the character set Kick channel
// URLs use in practice (Kick's API doesn't itself document a pattern,
// only the length).
var kickSlugPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,25}$`)

// normalizeTwitchChannel validates and lowercases a user-supplied Twitch
// channel name, stripping a leading "#" if present. This is the
// boundary check for every handler that accepts a raw channel string —
// it runs before the value is used to call Twitch's API or key the
// broadcaster-ID cache.
func normalizeTwitchChannel(raw string) (string, error) {
	channel := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "#"))
	if !twitchLoginPattern.MatchString(channel) {
		return "", fmt.Errorf("invalid twitch channel name %q: must be 4-25 characters (letters, numbers, underscores only)", raw)
	}
	return channel, nil
}

// normalizeKickChannel validates and lowercases a user-supplied Kick
// channel slug, mirroring normalizeTwitchChannel.
func normalizeKickChannel(raw string) (string, error) {
	channel := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "#"))
	if !kickSlugPattern.MatchString(channel) {
		return "", fmt.Errorf("invalid kick channel name %q: must be 1-25 characters (letters, numbers, underscores, hyphens only)", raw)
	}
	return channel, nil
}
