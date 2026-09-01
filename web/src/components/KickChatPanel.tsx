import { useState } from "react";
import { useKickChatStream, type KickDisplayMessage } from "../lib/useKickChatStream";
import { useKickSpamGroups } from "../lib/useKickSpamGroups";
import { useAutoScroll } from "../lib/useAutoScroll";
import { sendKickChatMessage } from "../lib/kickApi";
import { KickModerationControls } from "./KickModerationControls";
import { KickSpamGroupsPanel } from "./KickSpamGroupsPanel";
import { ChatBadges } from "./ChatBadges";
import { ChatMessageText } from "./ChatMessageText";
import { ChatComposer, type ReplyTarget } from "./ChatComposer";
import { KickChannelInfoMenu } from "./KickChannelInfoMenu";

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

/** A reply needs a real message to thread under, so system notices and
 * already-deleted messages aren't reply targets. */
function canReplyToMessage(m: KickDisplayMessage): boolean {
  return !m.system && !m.deleted;
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
  const [replyTo, setReplyTo] = useState<ReplyTarget | null>(null);
  const { ref: chatLogRef, onScroll } = useAutoScroll(messages);

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

        {viewerIsBroadcaster && <KickChannelInfoMenu channel={channel} />}

        {actionError && (
          <p className="error mod-error">
            {actionError}{" "}
            <button className="link-button" onClick={() => setActionError(null)}>
              dismiss
            </button>
          </p>
        )}

        <ul className="chat-log" ref={chatLogRef} onScroll={onScroll}>
          {messages.map((m, i) => (
            <li
              key={m.id || `${m.timestamp}-${i}`}
              className={m.system ? "chat-system" : m.deleted ? "chat-deleted" : undefined}
            >
              {m.system ? (
                <span className="chat-text">{m.text}</span>
              ) : (
                <>
                  {m.reply && (
                    <span className="chat-reply-context">
                      ↩ replying to <strong>{m.reply.displayName || m.reply.username}</strong>:{" "}
                      {m.reply.text}
                    </span>
                  )}
                  <ChatBadges badges={m.badges} />
                  <span className="chat-user" style={{ color: m.color || undefined }}>
                    {m.username}
                  </span>
                  <span className="chat-text">
                    {m.deleted ? (
                      "message deleted"
                    ) : (
                      <ChatMessageText text={m.text} fragments={m.fragments} />
                    )}
                  </span>
                  {canReplyToMessage(m) && (
                    <button
                      type="button"
                      className="chat-reply-button"
                      aria-label="Reply"
                      title="Reply"
                      onClick={() => setReplyTo({ id: m.id, username: m.username, text: m.text })}
                    >
                      ↩
                    </button>
                  )}
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

        <ChatComposer
          onSend={(message, replyToId) => sendKickChatMessage(channel, message, replyToId)}
          replyTo={replyTo}
          onCancelReply={() => setReplyTo(null)}
        />
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
