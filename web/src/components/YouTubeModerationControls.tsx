import { useState } from "react";
import {
  banYouTubeChatter,
  deleteYouTubeChatMessage,
  timeoutYouTubeChatter,
} from "../lib/youtubeApi";

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
        disabled={busy}
        onClick={() =>
          run(
            () => deleteYouTubeChatMessage(messageId),
            () => onDeleted(messageId),
          )
        }
      >
        Delete
      </button>
      <select
        className="mod-select"
        disabled={busy}
        value=""
        onChange={(e) => {
          const seconds = Number(e.target.value);
          e.target.value = "";
          if (seconds > 0) {
            run(() => timeoutYouTubeChatter(liveChatId, channelId, seconds));
          }
        }}
      >
        <option value="" disabled>
          Timeout...
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
        disabled={busy}
        onClick={() => run(() => banYouTubeChatter(liveChatId, channelId))}
      >
        Ban
      </button>
    </span>
  );
}
