import { useState } from "react";
import {
  uniqueSenders,
  type DuplicateMessageGroup,
} from "../lib/duplicateMessages";
import {
  banKickChatter,
  deleteKickChatMessage,
  timeoutKickChatter,
} from "../lib/kickApi";

const TIMEOUT_PRESETS: { label: string; minutes: number }[] = [
  { label: "1m", minutes: 1 },
  { label: "10m", minutes: 10 },
  { label: "1h", minutes: 60 },
  { label: "24h", minutes: 1440 },
  { label: "7d", minutes: 10080 },
];

type Props = {
  channel: string;
  groups: DuplicateMessageGroup<number>[];
  onDeleted: (messageId: string) => void;
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

type GroupRowProps = Pick<Props, "channel" | "onDeleted" | "onError"> & {
  group: DuplicateMessageGroup<number>;
};

function GroupRow({ channel, group, onDeleted, onError }: GroupRowProps) {
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
                async (entry) => {
                  await deleteKickChatMessage(entry.messageId);
                  onDeleted(entry.messageId);
                },
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
            const minutes = Number(e.target.value);
            e.target.value = "";
            if (minutes > 0) {
              withBusy(() =>
                runAll(
                  senders,
                  (s) => timeoutKickChatter(channel, s.userId, minutes),
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
            <option key={preset.minutes} value={preset.minutes}>
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
              runAll(senders, (s) => banKickChatter(channel, s.userId), onError),
            )
          }
        >
          Ban all
        </button>
      </div>
    </li>
  );
}

/** Shows groups of chatters who sent the same message within a short
 * window (a common raid/copypasta pattern), with bulk delete/timeout/ban
 * controls. Rendered under the Kick chat panel; hidden entirely when
 * nothing's been flagged. */
export function KickSpamGroupsPanel({ channel, groups, onDeleted, onError }: Props) {
  if (groups.length === 0) {
    return null;
  }

  return (
    <div className="card kick-panel spam-panel">
      <h2>Repeated messages</h2>
      <p className="hint">
        The same message sent by multiple chatters within 5 minutes — often a
        raid or copypasta. Act on everyone who sent it at once.
      </p>
      <ul className="spam-group-list">
        {groups.map((group) => (
          <GroupRow
            key={group.key}
            channel={channel}
            group={group}
            onDeleted={onDeleted}
            onError={onError}
          />
        ))}
      </ul>
    </div>
  );
}
