import { useState, type CSSProperties } from "react";
import { useChatStream, type DisplayMessage } from "../lib/useChatStream";
import { useAutoScroll } from "../lib/useAutoScroll";
import { formatClockTime } from "../lib/formatTime";
import { useShowTimestamps } from "../lib/settingsStore";
import { useTwitchSpamGroups } from "../lib/useTwitchSpamGroups";
import { fetchChatEmotes, fetchTwitchUser, sendChatMessage, unbanChatter } from "../lib/api";
import { ModerationControls } from "./ModerationControls";
import { ChatBadges } from "./ChatBadges";
import { ChatMessageText } from "./ChatMessageText";
import { ChatComposer, type ReplyTarget } from "./ChatComposer";
import { UserCard } from "./UserCard";
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
  if (m.deleted) return false;
  return canModerateSender(m, viewerIsBroadcaster);
}

/** Like canModerateMessage, but still true once the message is deleted
 * — a deleted message is often exactly when its sender needs an Unban. */
function canModerateSender(m: DisplayMessage, viewerIsBroadcaster: boolean): boolean {
  if (m.system) return false;
  if (m.broadcaster) return false;
  if (m.mod && !viewerIsBroadcaster) return false;
  return true;
}

/** A stable hue (0-359) per channel login, so each Shared Chat
 * participant keeps the same tag color for the whole session. */
function channelHue(login: string): number {
  let hash = 0;
  for (let i = 0; i < login.length; i++) {
    hash = (hash * 31 + login.charCodeAt(i)) | 0;
  }
  return Math.abs(hash) % 360;
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
  // Which message's user card (if any) is currently open — toggled by
  // clicking that message's username, one at a time. The card holds the
  // moderation controls, so they're never all shown at once.
  const [openCardFor, setOpenCardFor] = useState<string | null>(null);
  const { ref: chatLogRef, onScroll } = useAutoScroll(messages);
  const spamGroups = useTwitchSpamGroups(messages, viewerIsBroadcaster);
  const showTimestamps = useShowTimestamps();

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
                m.firstMessage ? "chat-firstmsg" : null,
                m.sourceChannel ? "chat-shared" : null,
              ]
                .filter(Boolean)
                .join(" ") || undefined}
              style={
                m.sourceChannel
                  ? ({ "--source-hue": channelHue(m.sourceChannel) } as CSSProperties)
                  : undefined
              }
            >
              {showTimestamps && (
                <span className="chat-timestamp">{formatClockTime(m.timestamp)}</span>
              )}
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
                  {m.sourceChannel && (
                    <span className="chat-source-line">
                      <span
                        className="chat-tag chat-tag-source"
                        title={`Sent in ${m.sourceChannelName || m.sourceChannel}'s chat (Shared Chat)`}
                      >
                        {m.sourceChannelName || m.sourceChannel}
                      </span>
                    </span>
                  )}
                  <ChatBadges badges={m.badges} />
                  {m.firstMessage && (
                    <span className="chat-tag chat-tag-first" title="Posted as a chat intro">
                      intro
                    </span>
                  )}
                  <button
                    type="button"
                    className="chat-user chat-user-button"
                    style={{ color: m.color || undefined }}
                    aria-expanded={openCardFor === m.id}
                    title="User card"
                    onClick={() => setOpenCardFor((cur) => (cur === m.id ? null : m.id))}
                  >
                    {m.displayName || m.username}
                  </button>
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
                  {openCardFor === m.id && (
                    <UserCard
                      platform="twitch"
                      scope={channel}
                      userId={m.userId}
                      username={m.displayName || m.username}
                      color={m.color}
                      messages={messages.filter((x) => !x.system && x.userId === m.userId)}
                      fetchInfo={() => fetchTwitchUser(m.userId)}
                      controls={
                        canModerate && canModerateMessage(m, viewerIsBroadcaster) ? (
                          <ModerationControls
                            channel={channel}
                            messageId={m.id}
                            userId={m.userId}
                            username={m.displayName || m.username}
                            onError={setActionError}
                          />
                        ) : undefined
                      }
                      unban={
                        canModerate && canModerateSender(m, viewerIsBroadcaster)
                          ? () => unbanChatter(channel, m.userId)
                          : undefined
                      }
                      onError={setActionError}
                      onClose={() => setOpenCardFor(null)}
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
