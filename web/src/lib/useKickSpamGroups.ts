import { useEffect, useMemo, useState } from "react";
import { groupDuplicateMessages, type DuplicateMessageGroup } from "./duplicateMessages";
import type { KickDisplayMessage } from "./useKickChatStream";

// How often to re-check the time window even when no new message has
// arrived, so a group correctly disappears once its messages age past
// DUPLICATE_WINDOW_MS rather than only when the next message triggers a
// recompute.
const RECHECK_INTERVAL_MS = 15_000;

/** useKickSpamGroups derives "likely raid/copypasta" groups from a live
 * Kick message list. viewerIsBroadcaster mirrors KickChatPanel's
 * per-message gating: never group the broadcaster's own messages, and
 * only a broadcaster (not a regular moderator) can bulk-act on other
 * moderators' messages. */
export function useKickSpamGroups(
  messages: KickDisplayMessage[],
  viewerIsBroadcaster: boolean,
): DuplicateMessageGroup<number>[] {
  const [recheckTick, setRecheckTick] = useState(0);

  useEffect(() => {
    const id = setInterval(() => setRecheckTick((n) => n + 1), RECHECK_INTERVAL_MS);
    return () => clearInterval(id);
  }, []);

  return useMemo(
    () =>
      groupDuplicateMessages(messages, {
        isEligible: (m) => {
          if (m.broadcaster) return false;
          if (m.mod && !viewerIsBroadcaster) return false;
          return true;
        },
      }),
    // recheckTick is intentionally a dependency with no direct use in
    // the body: it forces a recompute every RECHECK_INTERVAL_MS so a
    // group correctly disappears once its messages age past
    // DUPLICATE_WINDOW_MS, not only when a new message triggers one.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [messages, viewerIsBroadcaster, recheckTick],
  );
}
