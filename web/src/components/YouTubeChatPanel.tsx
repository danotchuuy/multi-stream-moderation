import { useState } from "react";
import {
  useYouTubeChatStream,
  type YouTubeDisplayMessage,
} from "../lib/useYouTubeChatStream";
import {
  fetchYouTubeUser,
  sendYouTubeChatMessage,
  unbanYouTubeChatter,
} from "../lib/youtubeApi";
import { useAutoScroll } from "../lib/useAutoScroll";
import { formatClockTime } from "../lib/formatTime";
import { useShowTimestamps } from "../lib/settingsStore";
import { YouTubeModerationControls } from "./YouTubeModerationControls";
import { ChatComposer } from "./ChatComposer";
import { UserCard } from "./UserCard";

type Props = {
  liveChatId: string;
  /** The broadcasting channel's name, for the panel header — falls back
   * to a generic label for a stream picked before this was added, whose
   * localStorage entry predates the field. */
  channelTitle: string;
  canModerate: boolean;
  viewerIsBroadcaster: boolean;
  onChangeStream: () => void;
  /** Called once the server reports the chat can't be streamed any more
   * (e.g. the broadcast ended), so the caller can stop restoring it on
   * future page loads. */
  onStreamEnded?: () => void;
};

function canModerateMessage(
  m: YouTubeDisplayMessage,
  viewerIsBroadcaster: boolean,
): boolean {
  if (m.deleted) return false;
  return canModerateSender(m, viewerIsBroadcaster);
}

/** Like canModerateMessage, but still true once the message is deleted
 * — a deleted message is often exactly when its sender needs an Unban. */
function canModerateSender(m: YouTubeDisplayMessage, viewerIsBroadcaster: boolean): boolean {
  if (m.system) return false;
  if (m.broadcaster) return false;
  if (m.mod && !viewerIsBroadcaster) return false;
  return true;
}

export function YouTubeChatPanel({
  liveChatId,
  channelTitle,
  canModerate,
  viewerIsBroadcaster,
  onChangeStream,
  onStreamEnded,
}: Props) {
  const { messages, connected, ended, streamError, markDeleted, addSentMessage } =
    useYouTubeChatStream(liveChatId, onStreamEnded);
  const [actionError, setActionError] = useState<string | null>(null);
  // Which message's user card (if any) is currently open — toggled by
  // clicking that message's username, one at a time. The card holds the
  // moderation controls, so they're never all shown at once.
  const [openCardFor, setOpenCardFor] = useState<string | null>(null);
  const { ref: chatLogRef, onScroll } = useAutoScroll(messages);
  const showTimestamps = useShowTimestamps();

  return (
    <div className="card chat-panel youtube-panel">
      <header className="chat-header">
        <div>
          <h2>
            {channelTitle || "Live chat"}{" "}
            <span className="platform-badge youtube-badge">YouTube</span>
          </h2>
          <span className={`status ${connected ? "connected" : "connecting"}`}>
            {connected ? "connected" : ended ? "disconnected" : "connecting..."}
          </span>
        </div>
        <button className="link-button" onClick={onChangeStream}>
          Change stream
        </button>
      </header>

      {streamError && <p className="error">{streamError}</p>}

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
              m.superChat ? "chat-superchat" : null,
            ]
              .filter(Boolean)
              .join(" ") || undefined}
          >
            {showTimestamps && (
              <span className="chat-timestamp">{formatClockTime(m.timestamp)}</span>
            )}
            {m.system ? (
              <span className="chat-text">{m.text}</span>
            ) : (
              <>
                <button
                  type="button"
                  className="chat-user chat-user-button"
                  aria-expanded={openCardFor === m.id}
                  title="User card"
                  onClick={() => setOpenCardFor((cur) => (cur === m.id ? null : m.id))}
                >
                  {m.displayName}
                </button>
                {m.superChat && (
                  <span className="chat-tag chat-tag-superchat" title="Super Chat">
                    💵 {m.superChat}
                  </span>
                )}
                <span className="chat-text">
                  {m.deleted ? "message deleted" : m.text}
                </span>
                {openCardFor === m.id && (
                  <UserCard
                    platform="youtube"
                    scope={liveChatId}
                    userId={m.channelId}
                    username={m.displayName}
                    messages={messages.filter((x) => !x.system && x.channelId === m.channelId)}
                    fetchInfo={() => fetchYouTubeUser(m.channelId)}
                    controls={
                      canModerate && canModerateMessage(m, viewerIsBroadcaster) ? (
                        <YouTubeModerationControls
                          liveChatId={liveChatId}
                          messageId={m.id}
                          channelId={m.channelId}
                          username={m.displayName}
                          onError={setActionError}
                          onDeleted={markDeleted}
                        />
                      ) : undefined
                    }
                    unban={
                      canModerate && canModerateSender(m, viewerIsBroadcaster)
                        ? () => unbanYouTubeChatter(liveChatId, m.channelId)
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
        {messages.length === 0 && !streamError && (
          <li className="chat-empty">Waiting for messages...</li>
        )}
      </ul>

      <ChatComposer
        onSend={async (message) => {
          const sent = await sendYouTubeChatMessage(liveChatId, message);
          addSentMessage(sent);
        }}
      />
    </div>
  );
}
