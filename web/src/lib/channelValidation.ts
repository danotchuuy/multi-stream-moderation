/** Mirrors internal/server/channel_validation.go — kept in sync by
 * hand, not generated. This is UX only (fast, in-page feedback before a
 * round-trip); the server re-validates authoritatively regardless. */

const TWITCH_LOGIN_PATTERN = /^[a-zA-Z0-9_]{4,25}$/;
const KICK_SLUG_PATTERN = /^[a-zA-Z0-9_-]{1,25}$/;

export const TWITCH_CHANNEL_HELP =
  "4-25 characters: letters, numbers, and underscores only.";
export const KICK_CHANNEL_HELP =
  "1-25 characters: letters, numbers, underscores, and hyphens only.";

function normalize(raw: string): string {
  return raw.trim().toLowerCase().replace(/^#/, "");
}

/** Returns the normalized channel name, or null if raw doesn't look
 * like a valid Twitch channel name. */
export function normalizeTwitchChannel(raw: string): string | null {
  const channel = normalize(raw);
  return TWITCH_LOGIN_PATTERN.test(channel) ? channel : null;
}

/** Returns the normalized channel slug, or null if raw doesn't look
 * like a valid Kick channel slug. */
export function normalizeKickChannel(raw: string): string | null {
  const channel = normalize(raw);
  return KICK_SLUG_PATTERN.test(channel) ? channel : null;
}
