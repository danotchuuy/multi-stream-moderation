/** Detects likely raid/copypasta spam: the same message text sent by
 * several different chatters within a short window. Pure and
 * platform-agnostic — takes any message shape with the fields below,
 * not just Kick's. */

export const DUPLICATE_WINDOW_MS = 5 * 60 * 1000;
export const MIN_DISTINCT_USERS = 2;

export type DuplicateSourceMessage = {
  id: string;
  text: string;
  userId: number;
  username: string;
  timestamp: number;
  deleted?: boolean;
  system?: boolean;
};

export type DuplicateGroupEntry = {
  messageId: string;
  userId: number;
  username: string;
  timestamp: number;
};

export type DuplicateMessageGroup = {
  /** Normalized (trimmed, lowercased) text — stable identity for the
   * group, e.g. as a React key. */
  key: string;
  /** Original casing of the most recently seen message with this text,
   * for display. */
  text: string;
  entries: DuplicateGroupEntry[];
};

/** groupDuplicateMessages buckets recent messages by normalized text and
 * returns only the buckets with at least MIN_DISTINCT_USERS distinct
 * senders within the last DUPLICATE_WINDOW_MS, newest activity first.
 * `isEligible`, if given, additionally excludes messages that shouldn't
 * be offered for bulk action (e.g. the broadcaster's own messages). */
export function groupDuplicateMessages<T extends DuplicateSourceMessage>(
  messages: T[],
  opts: { now?: number; isEligible?: (m: T) => boolean } = {},
): DuplicateMessageGroup[] {
  const now = opts.now ?? Date.now();
  const isEligible = opts.isEligible ?? (() => true);
  const cutoff = now - DUPLICATE_WINDOW_MS;

  const buckets = new Map<string, { text: string; entries: DuplicateGroupEntry[] }>();

  for (const m of messages) {
    if (m.deleted || m.system) continue;
    if (m.timestamp < cutoff) continue;
    if (!isEligible(m)) continue;

    const key = m.text.trim().toLowerCase();
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

  const groups: DuplicateMessageGroup[] = [];
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
export function uniqueSenders(
  group: DuplicateMessageGroup,
): { userId: number; username: string }[] {
  const seen = new Map<number, string>();
  for (const entry of group.entries) {
    if (!seen.has(entry.userId)) {
      seen.set(entry.userId, entry.username);
    }
  }
  return Array.from(seen, ([userId, username]) => ({ userId, username }));
}
