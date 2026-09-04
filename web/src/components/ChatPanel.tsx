import { useState } from "react";
import { useChatStream, type DisplayMessage } from "../lib/useChatStream";
import { useAutoScroll } from "../lib/useAutoScroll";
import { useTwitchSpamGroups } from "../lib/useTwitchSpamGroups";
import { fetchChatEmotes, sendChatMessage } from "../lib/api";
import { ModerationControls } from "./ModerationControls";
import { ChatBadges } from "./ChatBadges";
import { ChatMessageText } from "./ChatMessageText";
import { ChatComposer, type ReplyTarget } from "./ChatComposer";
import { TwitchChannelInfoMenu } from "./TwitchChannelInfoMenu";
import { TwitchSpamGroupsPanel } from "./TwitchSpamGroupsPanel";
import { AutomodQueuePanel } from "./AutomodQueuePanel";

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

/** A reply needs a real message to thread under, so system notices and
 * already-deleted messages aren't reply targets. */
function canReplyToMessage(m: DisplayMessage): boolean {
  return !m.system && !m.deleted;
}

export function ChatPanel({
  channel,
  canModerate,
  viewerIsBroadcaster,
  onChangeChannel,
}: Props) {
  const { messages, connected, automodQueue, markAutomodResolved } = useChatStream(channel);
  const [actionError, setActionError] = useState<string | null>(null);
  const [replyTo, setReplyTo] = useState<ReplyTarget | null>(null);
  // Which message's moderation controls (if any) are currently
  // expanded — toggled by clicking that message's username, one at a
  // time, rather than showing every eligible message's controls at once.
  const [openModFor, setOpenModFor] = useState<string | null>(null);
  const { ref: chatLogRef, onScroll } = useAutoScroll(messages);
  const spamGroups = useTwitchSpamGroups(messages, viewerIsBroadcaster);

  return (
    <>
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

        {viewerIsBroadcaster && <TwitchChannelInfoMenu channel={channel} />}

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
              className={[
                m.seq % 2 === 1 ? "chat-row-alt" : null,
                m.system ? "chat-system" : null,
                m.deleted ? "chat-deleted" : null,
                m.raid ? "chat-raid" : null,
              ]
                .filter(Boolean)
                .join(" ") || undefined}
            >
              {m.system ? (
                <span className="chat-text">
                  {m.raid ? "🎉 " : ""}
                  {m.text}
                </span>
              ) : (
                <>
                  {m.reply && (
                    <span className="chat-reply-context">
                      ↩ replying to <strong>{m.reply.displayName || m.reply.username}</strong>:{" "}
                      {m.reply.text}
                    </span>
                  )}
                  <ChatBadges badges={m.badges} />
                  {m.firstMessage && (
                    <span className="chat-tag chat-tag-first" title="Posted as a chat intro">
                      intro
                    </span>
                  )}
                  {canModerate && canModerateMessage(m, viewerIsBroadcaster) ? (
                    <button
                      type="button"
                      className="chat-user chat-user-button"
                      style={{ color: m.color || undefined }}
                      aria-expanded={openModFor === m.id}
                      title="Moderation options"
                      onClick={() => setOpenModFor((cur) => (cur === m.id ? null : m.id))}
                    >
                      {m.displayName || m.username}
                    </button>
                  ) : (
                    <span className="chat-user" style={{ color: m.color || undefined }}>
                      {m.displayName || m.username}
                    </span>
                  )}
                  {canModerate && canModerateMessage(m, viewerIsBroadcaster) && openModFor === m.id && (
                    <ModerationControls
                      channel={channel}
                      messageId={m.id}
                      userId={m.userId}
                      onError={setActionError}
                    />
                  )}
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
                      onClick={() =>
                        setReplyTo({ id: m.id, username: m.displayName || m.username, text: m.text })
                      }
                    >
                      ↩
                    </button>
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
          onSend={(message, replyToId) => sendChatMessage(channel, message, replyToId)}
          replyTo={replyTo}
          onCancelReply={() => setReplyTo(null)}
          fetchEmotes={() => fetchChatEmotes(channel)}
        />
      </div>

      {canModerate && (
        <AutomodQueuePanel
          queue={automodQueue}
          onResolved={markAutomodResolved}
          onError={setActionError}
        />
      )}

      {canModerate && (
        <TwitchSpamGroupsPanel channel={channel} groups={spamGroups} onError={setActionError} />
      )}
    </>
  );
}
