import { useState } from "react";
import {
  useYouTubeChatStream,
  type YouTubeDisplayMessage,
} from "../lib/useYouTubeChatStream";
import { sendYouTubeChatMessage } from "../lib/youtubeApi";
import { useAutoScroll } from "../lib/useAutoScroll";
import { YouTubeModerationControls } from "./YouTubeModerationControls";
import { ChatComposer } from "./ChatComposer";

type Props = {
  liveChatId: string;
  canModerate: boolean;
  viewerIsBroadcaster: boolean;
  onChangeStream: () => void;
};

function canModerateMessage(
  m: YouTubeDisplayMessage,
  viewerIsBroadcaster: boolean,
): boolean {
  if (m.system || m.deleted) return false;
  if (m.broadcaster) return false;
  if (m.mod && !viewerIsBroadcaster) return false;
  return true;
}

export function YouTubeChatPanel({
  liveChatId,
  canModerate,
  viewerIsBroadcaster,
  onChangeStream,
}: Props) {
  const { messages, connected, streamError, markDeleted } =
    useYouTubeChatStream(liveChatId);
  const [actionError, setActionError] = useState<string | null>(null);
  // Which message's moderation controls (if any) are currently
  // expanded — toggled by clicking that message's username, one at a
  // time, rather than showing every eligible message's controls at once.
  const [openModFor, setOpenModFor] = useState<string | null>(null);
  const { ref: chatLogRef, onScroll } = useAutoScroll(messages);

  return (
    <div className="card chat-panel youtube-panel">
      <header className="chat-header">
        <div>
          <h2>
            Live chat <span className="platform-badge youtube-badge">YouTube</span>
          </h2>
          <span className={`status ${connected ? "connected" : "connecting"}`}>
            {connected ? "connected" : "connecting..."}
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
              m.giftSub ? "chat-giftsub" : null,
            ]
              .filter(Boolean)
              .join(" ") || undefined}
          >
            {m.system ? (
              <span className="chat-text">{m.giftSub ? "🎁 " : ""}{m.text}</span>
            ) : (
              <>
                {canModerate && canModerateMessage(m, viewerIsBroadcaster) ? (
                  <button
                    type="button"
                    className="chat-user chat-user-button"
                    aria-expanded={openModFor === m.id}
                    title="Moderation options"
                    onClick={() => setOpenModFor((cur) => (cur === m.id ? null : m.id))}
                  >
                    {m.displayName}
                  </button>
                ) : (
                  <span className="chat-user">{m.displayName}</span>
                )}
                {canModerate && canModerateMessage(m, viewerIsBroadcaster) && openModFor === m.id && (
                  <YouTubeModerationControls
                    liveChatId={liveChatId}
                    messageId={m.id}
                    channelId={m.channelId}
                    onError={setActionError}
                    onDeleted={markDeleted}
                  />
                )}
                <span className="chat-text">
                  {m.deleted ? "message deleted" : m.text}
                </span>
              </>
            )}
          </li>
        ))}
        {messages.length === 0 && !streamError && (
          <li className="chat-empty">Waiting for messages...</li>
        )}
      </ul>

      <ChatComposer onSend={(message) => sendYouTubeChatMessage(liveChatId, message)} />
    </div>
  );
}
