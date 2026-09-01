import { useCallback, useEffect, useState } from "react";
import type { YouTubeChatMessage } from "./youtubeApi";

const MAX_MESSAGES = 200;

export type YouTubeDisplayMessage = YouTubeChatMessage & {
  deleted?: boolean;
};

function pushCapped(
  list: YouTubeDisplayMessage[],
  next: YouTubeDisplayMessage,
): YouTubeDisplayMessage[] {
  const trimmed = list.length >= MAX_MESSAGES ? list.slice(1) : list;
  return [...trimmed, next];
}

/** useYouTubeChatStream opens a server-sent-events connection to
 * /api/youtube/chat/stream, which itself polls YouTube's Live Chat API
 * server-side (YouTube has no push mechanism for chat). Unlike Twitch's
 * CLEARMSG/Kick's webhook ban events, YouTube gives this app no signal
 * when a message is deleted or a user banned elsewhere, so — like
 * Kick's message deletion — `markDeleted` lets the caller reflect only
 * its own successful actions. */
export function useYouTubeChatStream(liveChatId: string | null) {
  const [messages, setMessages] = useState<YouTubeDisplayMessage[]>([]);
  const [connected, setConnected] = useState(false);
  const [streamError, setStreamError] = useState<string | null>(null);

  useEffect(() => {
    setMessages([]);
    setConnected(false);
    setStreamError(null);
    if (!liveChatId) {
      return;
    }

    const source = new EventSource(
      `/api/youtube/chat/stream?liveChatId=${encodeURIComponent(liveChatId)}`,
    );
    source.onopen = () => setConnected(true);
    source.onerror = () => setConnected(false);

    source.addEventListener("message", (event) => {
      const msg = JSON.parse((event as MessageEvent).data) as YouTubeChatMessage;
      setMessages((prev) => pushCapped(prev, msg));
    });

    source.addEventListener("streamError", (event) => {
      const data = JSON.parse((event as MessageEvent).data) as { message?: string };
      setStreamError(data.message || "the live chat stream ended");
    });

    return () => {
      source.close();
    };
  }, [liveChatId]);

  const markDeleted = useCallback((messageId: string) => {
    setMessages((prev) =>
      prev.map((m) => (m.id === messageId ? { ...m, deleted: true } : m)),
    );
  }, []);

  return { messages, connected, streamError, markDeleted };
}
