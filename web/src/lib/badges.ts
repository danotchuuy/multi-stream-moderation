/** A chat badge as sent by the backend — the same shape for both Twitch
 * and Kick messages (see chatBadgeDTO in internal/server/handlers_chat.go),
 * so one badge-name-to-icon table and one rendering component cover
 * both chat panels. */
export type ChatBadge = {
  name: string;
  /** Distinguishes tiers within a badge — a subscriber badge's version
   * is their sub month count (Twitch) or tenure count (Kick). */
  version?: string;
  /** The exact badge image Twitch itself shows for this badge/version,
   * when known. Always unset for Kick, which has no equivalent lookup
   * in this app — those (and any unrecognized Twitch badge/version)
   * fall back to the icon table below. */
  imageUrl?: string;
};

type BadgeVisual = {
  icon: string;
  label: string;
};

// Badge names Twitch and Kick both use verbatim (or near enough) for
// the same concept. Anything not listed here still renders — as a
// generic tag icon with the raw name as its label — so an unrecognized
// or promotional badge is never silently dropped.
const KNOWN_BADGES: Record<string, BadgeVisual> = {
  broadcaster: { icon: "🎥", label: "Broadcaster" },
  moderator: { icon: "🗡️", label: "Moderator" },
  vip: { icon: "💎", label: "VIP" },
  subscriber: { icon: "⭐", label: "Subscriber" },
  founder: { icon: "🌱", label: "Founder" },
  partner: { icon: "✅", label: "Partner" },
  verified: { icon: "✅", label: "Verified" },
  staff: { icon: "🛠️", label: "Staff" },
  admin: { icon: "🛡️", label: "Admin" },
  global_mod: { icon: "🛡️", label: "Global Moderator" },
  turbo: { icon: "⚡", label: "Turbo" },
  premium: { icon: "👑", label: "Prime" },
  "sub-gifter": { icon: "🎁", label: "Gifter" },
  sub_gifter: { icon: "🎁", label: "Gifter" },
  gifter: { icon: "🎁", label: "Gifter" },
  "bits-leader": { icon: "💰", label: "Bits Leader" },
  bits: { icon: "💰", label: "Bits" },
  og: { icon: "🅾️", label: "OG" },
};

const FALLBACK_VISUAL: BadgeVisual = { icon: "🏷️", label: "" };

export function badgeVisual(badge: ChatBadge): BadgeVisual {
  return KNOWN_BADGES[badge.name] ?? { ...FALLBACK_VISUAL, label: badge.name };
}

/** A tooltip-friendly description, e.g. "Subscriber (12)". */
export function badgeTitle(badge: ChatBadge): string {
  const { label } = badgeVisual(badge);
  return badge.version ? `${label} (${badge.version})` : label;
}
