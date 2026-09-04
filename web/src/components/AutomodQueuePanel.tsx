import { useState } from "react";
import type { AutomodHeldMessage } from "../lib/useChatStream";
import { resolveAutomodMessage } from "../lib/api";
import { ChatMessageText } from "./ChatMessageText";

type Props = {
  queue: AutomodHeldMessage[];
  onResolved: (messageId: string) => void;
  onError: (message: string) => void;
};

function reasonLabel(m: AutomodHeldMessage): string {
  if (m.reason === "blocked_term") return "Blocked term";
  if (m.category) return `AutoMod · ${m.category} (level ${m.level ?? "?"})`;
  return "AutoMod";
}

type RowProps = Pick<Props, "onResolved" | "onError"> & {
  message: AutomodHeldMessage;
};

function HeldMessageRow({ message, onResolved, onError }: RowProps) {
  const [busy, setBusy] = useState(false);

  const resolve = async (action: "allow" | "deny") => {
    if (busy) return;
    setBusy(true);
    try {
      await resolveAutomodMessage(message.messageId, action);
      onResolved(message.messageId);
    } catch (err) {
      onError(err instanceof Error ? err.message : "automod action failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <li className="automod-item">
      <div className="automod-item-meta">
        <span className="chat-user">{message.displayName || message.username}</span>
        <span className="automod-reason">{reasonLabel(message)}</span>
      </div>
      <div className="automod-item-text">
        <ChatMessageText text={message.text} fragments={message.fragments} />
      </div>
      <div className="mod-controls">
        <button
          type="button"
          className="mod-button"
          disabled={busy}
          onClick={() => resolve("allow")}
        >
          Allow
        </button>
        <button
          type="button"
          className="mod-button mod-button-danger"
          disabled={busy}
          onClick={() => resolve("deny")}
        >
          Deny
        </button>
      </div>
    </li>
  );
}

/** Shows messages AutoMod is holding for review, with per-message
 * Allow/Deny controls. Rendered under the Twitch chat panel; the queue
 * only ever has anything in it for a viewer who's actually a moderator
 * (or the broadcaster) — Twitch itself only delivers these events to
 * whoever's subscribed — so this renders nothing at all for anyone
 * else, or once the queue is empty. */
export function AutomodQueuePanel({ queue, onResolved, onError }: Props) {
  if (queue.length === 0) {
    return null;
  }

  return (
    <div className="card automod-panel">
      <h2>AutoMod queue</h2>
      <p className="hint">Messages AutoMod is holding for review before they post to chat.</p>
      <ul className="automod-list">
        {queue.map((m) => (
          <HeldMessageRow
            key={m.messageId}
            message={m}
            onResolved={onResolved}
            onError={onError}
          />
        ))}
      </ul>
    </div>
  );
}
