import { useState } from "react";
import { banKickChatter, deleteKickChatMessage, timeoutKickChatter } from "../lib/kickApi";

// Kick's timeout duration is minutes (1-10080), not seconds like Twitch.
const TIMEOUT_PRESETS: { label: string; minutes: number }[] = [
  { label: "1m", minutes: 1 },
  { label: "10m", minutes: 10 },
  { label: "1h", minutes: 60 },
  { label: "24h", minutes: 1440 },
  { label: "7d", minutes: 10080 },
];

type Props = {
  channel: string;
  messageId: string;
  userId: number;
  onError: (message: string) => void;
  /** Kick sends no server-side event confirming a single message was
   * deleted (unlike Twitch's CLEARMSG), so the panel needs to be told
   * directly to reflect a successful delete. */
  onDeleted: (messageId: string) => void;
};

export function KickModerationControls({
  channel,
  messageId,
  userId,
  onError,
  onDeleted,
}: Props) {
  const [busy, setBusy] = useState(false);

  const run = async (action: () => Promise<void>, after?: () => void) => {
    if (busy) return;
    setBusy(true);
    try {
      await action();
      after?.();
    } catch (err) {
      onError(err instanceof Error ? err.message : "moderation action failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <span className="mod-controls">
      <button
        type="button"
        className="mod-button"
        aria-label="Delete message"
        title="Delete message"
        disabled={busy}
        onClick={() =>
          run(
            () => deleteKickChatMessage(messageId),
            () => onDeleted(messageId),
          )
        }
      >
        🗑
      </button>
      <select
        className="mod-select"
        aria-label="Timeout"
        title="Timeout"
        disabled={busy}
        value=""
        onChange={(e) => {
          const minutes = Number(e.target.value);
          e.target.value = "";
          if (minutes > 0) {
            run(() => timeoutKickChatter(channel, userId, minutes));
          }
        }}
      >
        <option value="" disabled>
          ⏱
        </option>
        {TIMEOUT_PRESETS.map((preset) => (
          <option key={preset.minutes} value={preset.minutes}>
            {preset.label}
          </option>
        ))}
      </select>
      <button
        type="button"
        className="mod-button mod-button-danger"
        aria-label="Ban"
        title="Ban"
        disabled={busy}
        onClick={() => run(() => banKickChatter(channel, userId))}
      >
        🔨
      </button>
    </span>
  );
}
