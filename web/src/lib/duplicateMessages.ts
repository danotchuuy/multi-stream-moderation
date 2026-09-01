/** Detects likely raid/copypasta spam: the same message text sent by
 * several different chatters within a short window. Pure and
 * platform-agnostic — takes any message shape with the fields below
 * (generic over the userId type so it fits both Twitch's string IDs and
 * Kick's numeric ones) and works the same for both chat panels. */

export const DUPLICATE_WINDOW_MS = 5 * 60 * 1000;
export const MIN_DISTINCT_USERS = 2;

// Matches emoji — pictographic characters, flag/regional-indicator
// pairs, skin-tone modifiers, the combining enclosing keycap used for
// sequences like 1️⃣, and the zero-width joiner/variation selector used
// to combine several of these into one glyph (e.g. a family or flag
// sequence) — so they can be stripped out before comparing two
// messages. Deliberately not \p{Emoji_Component} on its own: that
// property also covers plain digits, '#', and '*' (the base characters
// keycap sequences are built from), which would strip ordinary numbers
// out of unrelated text.
const EMOJI_PATTERN =
  /[\p{Extended_Pictographic}\u{1F1E6}-\u{1F1FF}\u{1F3FB}-\u{1F3FF}\u{200D}\u{FE0F}\u{20E3}]/gu;

/** Normalizes text for duplicate comparison: strips emoji (so, e.g.,
 * "gg 😂" and "gg 🎉" are still recognized as the same message),
 * collapses the whitespace that removing an inline emoji can leave
 * behind, trims, and lowercases. */
function normalizeForComparison(text: string): string {
  return text
    .replace(EMOJI_PATTERN, "")
    .replace(/\s+/g, " ")
    .trim()
    .toLowerCase();
}

export type DuplicateSourceMessage<U> = {
  id: string;
  text: string;
  userId: U;
  username: string;
  timestamp: number;
  deleted?: boolean;
  system?: boolean;
};

export type DuplicateGroupEntry<U> = {
  messageId: string;
  userId: U;
  username: string;
  timestamp: number;
};

export type DuplicateMessageGroup<U> = {
  /** Normalized (emoji-stripped, trimmed, lowercased) text — stable
   * identity for the group, e.g. as a React key. */
  key: string;
  /** Original text (emoji included) of the most recently seen message
   * with this normalized text, for display. */
  text: string;
  entries: DuplicateGroupEntry<U>[];
};

/** groupDuplicateMessages buckets recent messages by normalized text and
 * returns only the buckets with at least MIN_DISTINCT_USERS distinct
 * senders within the last DUPLICATE_WINDOW_MS, newest activity first.
 * `isEligible`, if given, additionally excludes messages that shouldn't
 * be offered for bulk action (e.g. the broadcaster's own messages). */
export function groupDuplicateMessages<U, T extends DuplicateSourceMessage<U>>(
  messages: T[],
  opts: { now?: number; isEligible?: (m: T) => boolean } = {},
): DuplicateMessageGroup<U>[] {
  const now = opts.now ?? Date.now();
  const isEligible = opts.isEligible ?? (() => true);
  const cutoff = now - DUPLICATE_WINDOW_MS;

  const buckets = new Map<string, { text: string; entries: DuplicateGroupEntry<U>[] }>();

  for (const m of messages) {
    if (m.deleted || m.system) continue;
    if (m.timestamp < cutoff) continue;
    if (!isEligible(m)) continue;

    const key = normalizeForComparison(m.text);
    if (!key) continue;

    let bucket = buckets.get(key);
    if (!bucket) {
      bucket = { text: m.text.trim(), entries: [] };
      buckets.set(key, bucket);
    }
    bucket.text = m.text.trim();
    bucket.entries.push({
      messageId: m.id,
      userId: m.userId,
      username: m.username,
      timestamp: m.timestamp,
    });
  }

  const groups: DuplicateMessageGroup<U>[] = [];
  for (const [key, bucket] of buckets) {
    const distinctUsers = new Set(bucket.entries.map((e) => e.userId));
    if (distinctUsers.size >= MIN_DISTINCT_USERS) {
      groups.push({ key, text: bucket.text, entries: bucket.entries });
    }
  }

  groups.sort((a, b) => {
    const aLatest = Math.max(...a.entries.map((e) => e.timestamp));
    const bLatest = Math.max(...b.entries.map((e) => e.timestamp));
    return bLatest - aLatest;
  });

  return groups;
}

/** The distinct {userId, username} senders in a group, in first-seen
 * order — the set to offer bulk ban/timeout against. */
export function uniqueSenders<U>(
  group: DuplicateMessageGroup<U>,
): { userId: U; username: string }[] {
  const seen = new Map<U, string>();
  for (const entry of group.entries) {
    if (!seen.has(entry.userId)) {
      seen.set(entry.userId, entry.username);
    }
  }
  return Array.from(seen, ([userId, username]) => ({ userId, username }));
}
