import { useState } from "react";
import { useKickChatStream, type KickDisplayMessage } from "../lib/useKickChatStream";
import { useKickSpamGroups } from "../lib/useKickSpamGroups";
import { KickModerationControls } from "./KickModerationControls";
import { KickSpamGroupsPanel } from "./KickSpamGroupsPanel";
import { ChatBadges } from "./ChatBadges";

type Props = {
  channel: string;
  canModerate: boolean;
  viewerIsBroadcaster: boolean;
  onChangeChannel: () => void;
};

export function canModerateMessage(
  m: KickDisplayMessage,
  viewerIsBroadcaster: boolean,
): boolean {
  if (m.system || m.deleted) return false;
  if (m.broadcaster) return false;
  if (m.mod && !viewerIsBroadcaster) return false;
  return true;
}

export function KickChatPanel({
  channel,
  canModerate,
  viewerIsBroadcaster,
  onChangeChannel,
}: Props) {
  const { messages, connected, markDeleted } = useKickChatStream(channel);
  const spamGroups = useKickSpamGroups(messages, viewerIsBroadcaster);
  const [actionError, setActionError] = useState<string | null>(null);

  return (
    <>
      <div className="card chat-panel kick-panel">
        <header className="chat-header">
          <div>
            <h2>
              {channel} <span className="platform-badge kick-badge">Kick</span>
            </h2>
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
                    {m.username}
                  </span>
                  <span className="chat-text">
                    {m.deleted ? "message deleted" : m.text}
                  </span>
                  {canModerate && canModerateMessage(m, viewerIsBroadcaster) && (
                    <KickModerationControls
                      channel={channel}
                      messageId={m.id}
                      userId={m.userId}
                      onError={setActionError}
                      onDeleted={markDeleted}
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

      {canModerate && (
        <KickSpamGroupsPanel
          channel={channel}
          groups={spamGroups}
          onDeleted={markDeleted}
          onError={setActionError}
        />
      )}
    </>
  );
}
