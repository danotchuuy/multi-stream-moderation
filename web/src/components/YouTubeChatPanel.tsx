import { useState } from "react";
import {
  useYouTubeChatStream,
  type YouTubeDisplayMessage,
} from "../lib/useYouTubeChatStream";
import { YouTubeModerationControls } from "./YouTubeModerationControls";

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
  if (m.deleted) return false;
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

      <ul className="chat-log">
        {messages.map((m, i) => (
          <li
            key={m.id || `${m.timestamp}-${i}`}
            className={m.deleted ? "chat-deleted" : undefined}
          >
            <span className="chat-user">{m.displayName}</span>
            <span className="chat-text">
              {m.deleted ? "message deleted" : m.text}
            </span>
            {canModerate && canModerateMessage(m, viewerIsBroadcaster) && (
              <YouTubeModerationControls
                liveChatId={liveChatId}
                messageId={m.id}
                channelId={m.channelId}
                onError={setActionError}
                onDeleted={markDeleted}
              />
            )}
          </li>
        ))}
        {messages.length === 0 && !streamError && (
          <li className="chat-empty">Waiting for messages...</li>
        )}
      </ul>
    </div>
  );
}
