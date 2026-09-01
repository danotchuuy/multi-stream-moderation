import { useState } from "react";
import {
  uniqueSenders,
  type DuplicateMessageGroup,
} from "../lib/duplicateMessages";
import {
  banChatter,
  deleteChatMessage,
  timeoutChatter,
} from "../lib/api";

const TIMEOUT_PRESETS: { label: string; seconds: number }[] = [
  { label: "1m", seconds: 60 },
  { label: "10m", seconds: 600 },
  { label: "1h", seconds: 3600 },
  { label: "24h", seconds: 86400 },
];

type Props = {
  channel: string;
  groups: DuplicateMessageGroup<string>[];
  onError: (message: string) => void;
};

async function runAll<T>(
  items: T[],
  action: (item: T) => Promise<void>,
  onError: (message: string) => void,
): Promise<void> {
  const results = await Promise.allSettled(items.map(action));
  const failed = results.filter(
    (r): r is PromiseRejectedResult => r.status === "rejected",
  );
  if (failed.length > 0) {
    const first =
      failed[0].reason instanceof Error
        ? failed[0].reason.message
        : "action failed";
    onError(
      failed.length === items.length
        ? first
        : `${failed.length} of ${items.length} failed: ${first}`,
    );
  }
}

type GroupRowProps = Pick<Props, "channel" | "onError"> & {
  group: DuplicateMessageGroup<string>;
};

function GroupRow({ channel, group, onError }: GroupRowProps) {
  const [busy, setBusy] = useState(false);
  const senders = uniqueSenders(group);

  const withBusy = async (fn: () => Promise<void>) => {
    if (busy) return;
    setBusy(true);
    try {
      await fn();
    } finally {
      setBusy(false);
    }
  };

  return (
    <li className="spam-group">
      <div className="spam-group-text">&ldquo;{group.text}&rdquo;</div>
      <div className="spam-group-meta">
        {senders.length} users &middot; {group.entries.length} messages
      </div>
      <div className="spam-group-users">
        {senders.map((s) => (
          <span key={s.userId} className="spam-group-user">
            {s.username}
          </span>
        ))}
      </div>
      <div className="mod-controls">
        <button
          type="button"
          className="mod-button"
          disabled={busy}
          onClick={() =>
            withBusy(() =>
              runAll(
                group.entries,
                (entry) => deleteChatMessage(channel, entry.messageId),
                onError,
              ),
            )
          }
        >
          Delete all
        </button>
        <select
          className="mod-select"
          disabled={busy}
          value=""
          onChange={(e) => {
            const seconds = Number(e.target.value);
            e.target.value = "";
            if (seconds > 0) {
              withBusy(() =>
                runAll(
                  senders,
                  (s) => timeoutChatter(channel, s.userId, seconds),
                  onError,
                ),
              );
            }
          }}
        >
          <option value="" disabled>
            Timeout all...
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
          onClick={() =>
            withBusy(() =>
              runAll(senders, (s) => banChatter(channel, s.userId), onError),
            )
          }
        >
          Ban all
        </button>
      </div>
    </li>
  );
}

/** Shows groups of chatters who sent the same message (emoji aside —
 * see duplicateMessages.ts) within a short window (a common
 * raid/copypasta pattern), with bulk delete/timeout/ban controls.
 * Rendered under the Twitch chat panel; hidden entirely when nothing's
 * been flagged. The Kick equivalent is KickSpamGroupsPanel — Twitch
 * unlike Kick reflects a deletion via its own CLEARMSG EventSub event,
 * so this needs no onDeleted callback to update the chat log itself. */
export function TwitchSpamGroupsPanel({ channel, groups, onError }: Props) {
  if (groups.length === 0) {
    return null;
  }

  return (
    <div className="card spam-panel">
      <h2>Repeated messages</h2>
      <p className="hint">
        The same message sent by multiple chatters within 5 minutes — often a
        raid or copypasta. Act on everyone who sent it at once.
      </p>
      <ul className="spam-group-list">
        {groups.map((group) => (
          <GroupRow key={group.key} channel={channel} group={group} onError={onError} />
        ))}
      </ul>
    </div>
  );
}
