import { useState } from "react";
import {
  banYouTubeChatter,
  deleteYouTubeChatMessage,
  timeoutYouTubeChatter,
  unbanYouTubeChatter,
} from "../lib/youtubeApi";
import { recordBan } from "../lib/moderationActionsStore";

// YouTube's ban duration is seconds, like Twitch (Kick uses minutes).
const TIMEOUT_PRESETS: { label: string; seconds: number }[] = [
  { label: "1m", seconds: 60 },
  { label: "10m", seconds: 600 },
  { label: "1h", seconds: 3600 },
  { label: "24h", seconds: 86400 },
];

type Props = {
  liveChatId: string;
  messageId: string;
  channelId: string;
  /** Labels the undo toast and user card entry. */
  username: string;
  onError: (message: string) => void;
  /** YouTube gives this app no server-side event confirming a message
   * was deleted (like Kick), so the panel needs to be told directly to
   * reflect a successful delete. */
  onDeleted: (messageId: string) => void;
};

export function YouTubeModerationControls({
  liveChatId,
  messageId,
  channelId,
  username,
  onError,
  onDeleted,
}: Props) {
  const [busy, setBusy] = useState(false);
  const target = { platform: "youtube" as const, scope: liveChatId, userId: channelId, username };
  const unban = () => unbanYouTubeChatter(liveChatId, channelId);

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
            () => deleteYouTubeChatMessage(messageId),
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
          const seconds = Number(e.target.value);
          e.target.value = "";
          if (seconds > 0) {
            const label = TIMEOUT_PRESETS.find((p) => p.seconds === seconds)?.label;
            run(async () => {
              await timeoutYouTubeChatter(liveChatId, channelId, seconds);
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
            await banYouTubeChatter(liveChatId, channelId);
            recordBan(target, "ban", unban);
          })
        }
      >
        🔨
      </button>
    </span>
  );
}
