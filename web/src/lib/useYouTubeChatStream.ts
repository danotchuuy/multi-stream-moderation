import { useCallback, useEffect, useState } from "react";
import type { YouTubeChatMessage } from "./youtubeApi";

const MAX_MESSAGES = 1000;

export type YouTubeDisplayMessage = YouTubeChatMessage & {
  deleted?: boolean;
  /** Set on the synthetic notice for a gift membership / community
   * gift, distinguishing it from a real chat message. */
  system?: boolean;
  giftSub?: boolean;
  /** Monotonically increasing arrival order, stamped by pushCapped —
   * see the matching field on DisplayMessage in useChatStream.ts for
   * why this exists instead of using a message's position in the
   * array for the chat log's alternating row background. */
  seq: number;
};

/** A single gift or a batch gifted at once — just the gifter and how
 * many, never who each one went to (see giftSubDTO in
 * internal/server/handlers_chat.go). */
type GiftSubEvent = {
  displayName: string;
  count: number;
};

function pushCapped(
  list: YouTubeDisplayMessage[],
  next: Omit<YouTubeDisplayMessage, "seq">,
): YouTubeDisplayMessage[] {
  const seq = (list[list.length - 1]?.seq ?? 0) + 1;
  const trimmed = list.length >= MAX_MESSAGES ? list.slice(1) : list;
  return [...trimmed, { ...next, seq }];
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

    source.addEventListener("giftsub", (event) => {
      const gift = JSON.parse((event as MessageEvent).data) as GiftSubEvent;
      const notice: Omit<YouTubeDisplayMessage, "seq"> = {
        id: `giftsub-${Date.now()}-${gift.displayName}`,
        text: `${gift.displayName || "Someone"} gifted ${gift.count} membership${gift.count === 1 ? "" : "s"}!`,
        displayName: "",
        channelId: "",
        profileImage: "",
        mod: false,
        subscriber: false,
        broadcaster: false,
        timestamp: Date.now(),
        system: true,
        giftSub: true,
      };
      setMessages((prev) => pushCapped(prev, notice));
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
