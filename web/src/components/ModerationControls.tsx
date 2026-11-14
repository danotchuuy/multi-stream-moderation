import { useState } from "react";
import { banChatter, deleteChatMessage, timeoutChatter, unbanChatter } from "../lib/api";
import { recordBan } from "../lib/moderationActionsStore";

const TIMEOUT_PRESETS: { label: string; seconds: number }[] = [
  { label: "1m", seconds: 60 },
  { label: "10m", seconds: 600 },
  { label: "1h", seconds: 3600 },
  { label: "24h", seconds: 86400 },
];

type Props = {
  channel: string;
  messageId: string;
  userId: string;
  /** Labels the undo toast and user card entry. */
  username: string;
  onError: (message: string) => void;
};

/** Delete / timeout / ban controls for one chat message. Rendered only
 * when the viewer has moderation access to the channel and Twitch's own
 * rules allow acting on this particular message (see
 * ChatPanel.canModerateMessage). Twitch still enforces this
 * authoritatively server-side — these are just the client-side controls
 * for making the calls. */
export function ModerationControls({ channel, messageId, userId, username, onError }: Props) {
  const [busy, setBusy] = useState(false);
  const target = { platform: "twitch" as const, scope: channel, userId, username };
  const unban = () => unbanChatter(channel, userId);

  const run = async (action: () => Promise<void>) => {
    if (busy) return;
    setBusy(true);
    try {
      await action();
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
        onClick={() => run(() => deleteChatMessage(channel, messageId))}
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
          const seconds = Number(e.target.value);
          e.target.value = "";
          if (seconds > 0) {
            const label = TIMEOUT_PRESETS.find((p) => p.seconds === seconds)?.label;
            run(async () => {
              await timeoutChatter(channel, userId, seconds);
              recordBan(target, "timeout", unban, label);
            });
          }
        }}
      >
        <option value="" disabled>
          ⏱
        </option>
        {TIMEOUT_PRESETS.map((preset) => (
          <option key={preset.seconds} value={preset.seconds}>
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
        onClick={() =>
          run(async () => {
            await banChatter(channel, userId);
            recordBan(target, "ban", unban);
          })
        }
      >
        🔨
      </button>
    </span>
  );
}
