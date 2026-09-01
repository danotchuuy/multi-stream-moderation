import { useState } from "react";
import { useChatStream, type DisplayMessage } from "../lib/useChatStream";
import { ModerationControls } from "./ModerationControls";
import { ChatBadges } from "./ChatBadges";

type Props = {
  channel: string;
  /** Whether the signed-in user moderates (or broadcasts) this channel
   * at all. When false, no moderation controls are shown. */
  canModerate: boolean;
  /** Whether the signed-in user is this channel's broadcaster, who —
   * unlike a regular moderator — can act on other moderators' messages
   * too. */
  viewerIsBroadcaster: boolean;
  onChangeChannel: () => void;
};

/** Mirrors what Twitch itself allows: nobody can moderate the
 * broadcaster, and a regular moderator can't moderate another
 * moderator. The API call would fail anyway; this just keeps the
 * controls from being offered for actions that would just error. */
function canModerateMessage(m: DisplayMessage, viewerIsBroadcaster: boolean): boolean {
  if (m.system || m.deleted) return false;
  if (m.broadcaster) return false;
  if (m.mod && !viewerIsBroadcaster) return false;
  return true;
}

export function ChatPanel({
  channel,
  canModerate,
  viewerIsBroadcaster,
  onChangeChannel,
}: Props) {
  const { messages, connected } = useChatStream(channel);
  const [actionError, setActionError] = useState<string | null>(null);

  return (
    <div className="card chat-panel">
      <header className="chat-header">
        <div>
          <h2>#{channel}</h2>
          <span className={`status ${connected ? "connected" : "connecting"}`}>
            {connected ? "connected" : "connecting..."}
          </span>
        </div>
        <button className="link-button" onClick={onChangeChannel}>
          Change channel
        </button>
      </header>

      {actionError && (
        <p className="error mod-error">
          {actionError}{" "}
          <button className="link-button" onClick={() => setActionError(null)}>
            dismiss
          </button>
        </p>
      )}

      <ul className="chat-log">
        {messages.map((m, i) => (
          <li
            key={m.id || `${m.timestamp}-${i}`}
            className={m.system ? "chat-system" : m.deleted ? "chat-deleted" : undefined}
          >
            {m.system ? (
              <span className="chat-text">{m.text}</span>
            ) : (
              <>
                <ChatBadges badges={m.badges} />
                <span className="chat-user" style={{ color: m.color || undefined }}>
                  {m.displayName || m.username}
                </span>
                <span className="chat-text">
                  {m.deleted ? "message deleted" : m.text}
                </span>
                {canModerate && canModerateMessage(m, viewerIsBroadcaster) && (
                  <ModerationControls
                    channel={channel}
                    messageId={m.id}
                    userId={m.userId}
                    onError={setActionError}
                  />
                )}
              </>
            )}
          </li>
        ))}
        {messages.length === 0 && (
          <li className="chat-empty">Waiting for messages...</li>
        )}
      </ul>
    </div>
  );
}
