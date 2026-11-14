import { useSyncExternalStore } from "react";

const MAX_ACTIONS = 500;

export type ModPlatform = "twitch" | "kick" | "youtube";

/** One ban, timeout or unban this viewer made from the app, kept for
 * the rest of the page session. The undo toast (UndoToasts) shows the
 * newest ones with an Undo button, and the user card (UserCard) lists
 * every action taken on the chatter it's showing. */
export type ModAction = {
  id: string;
  platform: ModPlatform;
  /** The channel login (Twitch/Kick) or live chat ID (YouTube) — scopes
   * a user's actions to the chat they happened in. */
  scope: string;
  /** The platform's own user ID (YouTube: channel ID). */
  userId: string;
  username: string;
  kind: "ban" | "timeout" | "unban";
  /** Human-readable timeout length, e.g. "10m". */
  duration?: string;
  timestamp: number;
  /** Lifts this ban/timeout. Absent on an unban. */
  undo?: () => Promise<void>;
  /** Set once the action has been undone (from the toast or the card),
   * so it isn't offered for undo twice. */
  undone?: boolean;
};

let actions: ModAction[] = [];
const listeners = new Set<() => void>();

function emit() {
  for (const listener of listeners) listener();
}

let nextId = 0;

export function recordModAction(action: Omit<ModAction, "id" | "timestamp">): ModAction {
  const entry: ModAction = { ...action, id: `modaction-${++nextId}`, timestamp: Date.now() };
  actions = actions.length >= MAX_ACTIONS ? [...actions.slice(1), entry] : [...actions, entry];
  emit();
  return entry;
}

/** Marks every still-active ban/timeout on this user in this chat as
 * undone — an unban lifts all of them at once, whichever one it was
 * started from. */
export function markUserUnbanned(platform: ModPlatform, scope: string, userId: string) {
  let changed = false;
  actions = actions.map((a) => {
    if (
      !a.undone &&
      a.kind !== "unban" &&
      a.platform === platform &&
      a.scope === scope &&
      a.userId === userId
    ) {
      changed = true;
      return { ...a, undone: true };
    }
    return a;
  });
  if (changed) emit();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

function getSnapshot() {
  return actions;
}

export function useModActions(): ModAction[] {
  return useSyncExternalStore(subscribe, getSnapshot);
}

/** Records a ban or timeout and wires its undo to `unban`, which also
 * records the matching unban entry when run. */
export function recordBan(
  base: Omit<ModAction, "id" | "timestamp" | "kind" | "undo" | "undone">,
  kind: "ban" | "timeout",
  unban: () => Promise<void>,
  duration?: string,
) {
  recordModAction({
    ...base,
    kind,
    duration,
    undo: () => runUnban(base, unban),
  });
}

/** Calls `unban`, then records it and clears every active ban/timeout
 * on the user. Throws (recording nothing) if the unban fails. */
export async function runUnban(
  base: Omit<ModAction, "id" | "timestamp" | "kind" | "undo" | "undone">,
  unban: () => Promise<void>,
) {
  await unban();
  markUserUnbanned(base.platform, base.scope, base.userId);
  recordModAction({ ...base, kind: "unban" });
}
