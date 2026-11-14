import { useEffect, useState, type ReactNode } from "react";
import type { UserInfo } from "../lib/api";
import { formatClockTime } from "../lib/formatTime";
import { runUnban, useModActions, type ModPlatform } from "../lib/moderationActionsStore";
import { describeModAction } from "./UndoToasts";

/** How many of the chatter's recent messages the card lists. */
const MAX_HISTORY = 20;

export type UserCardMessage = {
  id: string;
  text: string;
  timestamp: number;
  deleted?: boolean;
};

type Props = {
  platform: ModPlatform;
  /** Channel login (Twitch/Kick) or live chat ID (YouTube). */
  scope: string;
  userId: string;
  username: string;
  color?: string;
  /** Every message from this chatter the panel currently holds, oldest
   * first. No platform has a chat-history API, so this is all there is. */
  messages: UserCardMessage[];
  /** Looks up account details. Absent for Kick, whose API doesn't
   * expose an account creation date. */
  fetchInfo?: () => Promise<UserInfo>;
  /** Delete/timeout/ban controls for the message the card was opened
   * from; absent when this viewer can't moderate that chatter. */
  controls?: ReactNode;
  /** Lifts a ban/timeout on this chatter; absent when this viewer can't
   * moderate them. */
  unban?: () => Promise<void>;
  onError: (message: string) => void;
  onClose: () => void;
};

function formatAccountAge(createdAt: number): string {
  const date = new Date(createdAt).toLocaleDateString([], {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
  const days = Math.floor((Date.now() - createdAt) / 86_400_000);
  let age: string;
  if (days < 1) age = "today";
  else if (days < 60) age = `${days} day${days === 1 ? "" : "s"} ago`;
  else if (days < 730) age = `${Math.floor(days / 30)} months ago`;
  else age = `${Math.floor(days / 365)} years ago`;
  return `${date} (${age})`;
}

/** UserCard expands under a chat message when its sender's name is
 * clicked: account age where the platform exposes it, the chatter's
 * recent messages in this chat, every ban/timeout/unban this viewer has
 * made on them this session, and the moderation controls (plus Unban). */
export function UserCard({
  platform,
  scope,
  userId,
  username,
  color,
  messages,
  fetchInfo,
  controls,
  unban,
  onError,
  onClose,
}: Props) {
  const [info, setInfo] = useState<UserInfo | null>(null);
  const [infoError, setInfoError] = useState<string | null>(null);
  const [unbanning, setUnbanning] = useState(false);
  const allActions = useModActions();
  const actions = allActions.filter(
    (a) => a.platform === platform && a.scope === scope && a.userId === userId,
  );

  useEffect(() => {
    if (!fetchInfo) return;
    let cancelled = false;
    fetchInfo()
      .then((result) => !cancelled && setInfo(result))
      .catch((err) => !cancelled && setInfoError(err instanceof Error ? err.message : "lookup failed"));
    return () => {
      cancelled = true;
    };
    // fetchInfo is a fresh closure every render, so it's deliberately
    // left out: refetch only when the card switches to another user.
  }, [platform, userId]);

  const handleUnban = async () => {
    if (!unban || unbanning) return;
    setUnbanning(true);
    try {
      await runUnban({ platform, scope, userId, username }, unban);
    } catch (err) {
      onError(err instanceof Error ? err.message : "unban failed");
    } finally {
      setUnbanning(false);
    }
  };

  const history = messages.slice(-MAX_HISTORY);

  return (
    <div className="user-card" role="region" aria-label={`${username}'s user card`}>
      <div className="user-card-header">
        {info?.profileImage && (
          <img className="user-card-avatar" src={info.profileImage} alt="" width={32} height={32} />
        )}
        <div className="user-card-title">
          <strong style={{ color: color || undefined }}>{username}</strong>
          {fetchInfo && (
            <span className="user-card-meta">
              {info?.createdAt
                ? `Account created ${formatAccountAge(info.createdAt)}`
                : infoError
                  ? `Account details unavailable: ${infoError}`
                  : info
                    ? null
                    : "Loading account details…"}
            </span>
          )}
        </div>
        <button
          type="button"
          className="user-card-close"
          aria-label="Close user card"
          title="Close"
          onClick={onClose}
        >
          ×
        </button>
      </div>

      {(controls || unban) && (
        <div className="user-card-actions">
          {controls}
          {unban && (
            <button
              type="button"
              className="mod-button"
              disabled={unbanning}
              onClick={handleUnban}
              title="Lift a ban or end a timeout early"
            >
              Unban
            </button>
          )}
        </div>
      )}

      <div className="user-card-section">
        <h4>Your actions this session</h4>
        {actions.length === 0 ? (
          <p className="user-card-empty">None.</p>
        ) : (
          <ul className="user-card-list">
            {actions.map((a) => (
              <li key={a.id} className={a.undone ? "user-card-undone" : undefined}>
                <span className="chat-timestamp">{formatClockTime(a.timestamp)}</span>
                {describeModAction(a)}
                {a.undone && " (lifted)"}
              </li>
            ))}
          </ul>
        )}
      </div>

      <div className="user-card-section">
        <h4>
          Recent messages here ({messages.length}
          {messages.length > history.length ? `, showing last ${history.length}` : ""})
        </h4>
        <ul className="user-card-list">
          {history.map((m) => (
            <li key={m.id} className={m.deleted ? "user-card-undone" : undefined}>
              <span className="chat-timestamp">{formatClockTime(m.timestamp)}</span>
              {m.deleted ? <em>message deleted</em> : m.text}
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
