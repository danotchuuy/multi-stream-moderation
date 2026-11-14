import { useEffect, useState } from "react";
import { useModActions, type ModAction, type ModPlatform } from "../lib/moderationActionsStore";

/** How long a ban/timeout stays undoable from its toast. The user card
 * keeps offering Unban after that. */
const UNDO_WINDOW_MS = 10_000;

export const PLATFORM_LABEL: Record<ModPlatform, string> = {
  twitch: "Twitch",
  kick: "Kick",
  youtube: "YouTube",
};

export function describeModAction(a: ModAction): string {
  switch (a.kind) {
    case "ban":
      return "Banned";
    case "timeout":
      return a.duration ? `Timed out (${a.duration})` : "Timed out";
    case "unban":
      return "Unbanned";
  }
}

function UndoToast({ action, onDismiss }: { action: ModAction; onDismiss: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const undo = async () => {
    if (!action.undo || busy) return;
    setBusy(true);
    setError(null);
    try {
      await action.undo();
    } catch (err) {
      setError(err instanceof Error ? err.message : "undo failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <li className="undo-toast" role="status">
      <span className="undo-toast-text">
        <span className={`platform-badge ${action.platform}-badge`}>
          {PLATFORM_LABEL[action.platform]}
        </span>{" "}
        {describeModAction(action)} <strong>{action.username}</strong>
        {error && <span className="error"> — {error}</span>}
      </span>
      <button type="button" className="undo-toast-button" disabled={busy} onClick={undo}>
        Undo
      </button>
      <button
        type="button"
        className="undo-toast-dismiss"
        aria-label="Dismiss"
        title="Dismiss"
        onClick={onDismiss}
      >
        ×
      </button>
    </li>
  );
}

/** UndoToasts shows a short-lived Undo toast for each ban or timeout
 * this viewer makes, from any platform's panel, stacked at the bottom
 * of the window. */
export function UndoToasts() {
  const actions = useModActions();
  const [now, setNow] = useState(() => Date.now());
  const [dismissed, setDismissed] = useState<Set<string>>(() => new Set());

  const visible = actions.filter(
    (a) =>
      a.undo &&
      !a.undone &&
      !dismissed.has(a.id) &&
      now - a.timestamp < UNDO_WINDOW_MS,
  );
  const newestTimestamp = actions[actions.length - 1]?.timestamp ?? 0;

  // Tick only while a toast could still be showing, so an idle page
  // doesn't re-render every second.
  useEffect(() => {
    if (Date.now() - newestTimestamp >= UNDO_WINDOW_MS) return;
    setNow(Date.now());
    const timer = setInterval(() => {
      const t = Date.now();
      setNow(t);
      if (t - newestTimestamp >= UNDO_WINDOW_MS) clearInterval(timer);
    }, 500);
    return () => clearInterval(timer);
  }, [newestTimestamp]);

  if (visible.length === 0) return null;

  return (
    <ul className="undo-toasts" aria-live="polite">
      {visible.map((a) => (
        <UndoToast
          key={a.id}
          action={a}
          onDismiss={() => setDismissed((prev) => new Set(prev).add(a.id))}
        />
      ))}
    </ul>
  );
}
