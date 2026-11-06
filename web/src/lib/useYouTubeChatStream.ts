import { useCallback, useEffect, useRef, useState } from "react";
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
 * many, never who each one went to (see youtubeGiftSubDTO in
 * internal/server/youtube_stream.go). */
type GiftSubEvent = {
  id: string;
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
 * /api/youtube/chat/stream, which relays one shared server-side
 * liveChatMessages.streamList per chat to every tab, replaying recent
 * messages on (re)connect — hence the de-duplication by ID below. A fatal streamError (chat
 * ended, access revoked, bad ID) closes the EventSource — otherwise it
 * would auto-reconnect every few seconds and re-open a doomed
 * streamList call each time, burning API quota — and calls onFatal.
 * Unlike Twitch's
 * CLEARMSG/Kick's webhook ban events, YouTube gives this app no signal
 * when a message is deleted or a user banned elsewhere, so — like
 * Kick's message deletion — `markDeleted` lets the caller reflect only
 * its own successful actions. */
export function useYouTubeChatStream(
  liveChatId: string | null,
  onFatal?: () => void,
) {
  const [messages, setMessages] = useState<YouTubeDisplayMessage[]>([]);
  const [connected, setConnected] = useState(false);
  const [ended, setEnded] = useState(false);
  const [streamError, setStreamError] = useState<string | null>(null);
  // Kept in a ref so a caller passing a fresh callback each render
  // doesn't tear down and re-open the stream.
  const onFatalRef = useRef(onFatal);
  useEffect(() => {
    onFatalRef.current = onFatal;
  }, [onFatal]);

  useEffect(() => {
    setMessages([]);
    setConnected(false);
    setEnded(false);
    setStreamError(null);
    if (!liveChatId) {
      return;
    }

    const source = new EventSource(
      `/api/youtube/chat/stream?liveChatId=${encodeURIComponent(liveChatId)}`,
    );
    source.onopen = () => {
      setConnected(true);
      setStreamError(null);
    };
    source.onerror = () => setConnected(false);

    source.addEventListener("message", (event) => {
      setStreamError(null);
      const msg = JSON.parse((event as MessageEvent).data) as YouTubeChatMessage;
      // A message this viewer sent may already be showing, added
      // optimistically by addSentMessage — skip the echo rather than
      // showing it twice.
      setMessages((prev) =>
        prev.some((m) => m.id === msg.id) ? prev : pushCapped(prev, msg),
      );
    });

    source.addEventListener("giftsub", (event) => {
      const gift = JSON.parse((event as MessageEvent).data) as GiftSubEvent;
      const notice: Omit<YouTubeDisplayMessage, "seq"> = {
        id: `giftsub-${gift.id}`,
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
      // The server replays its recent backlog whenever EventSource
      // reconnects, so skip a gift already shown.
      setMessages((prev) =>
        prev.some((m) => m.id === notice.id) ? prev : pushCapped(prev, notice),
      );
    });

    source.addEventListener("streamError", (event) => {
      const data = JSON.parse((event as MessageEvent).data) as {
        message?: string;
        fatal?: boolean;
      };
      setStreamError(data.message || "the live chat stream ended");
      if (data.fatal) {
        source.close();
        setConnected(false);
        setEnded(true);
        onFatalRef.current?.();
      }
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

  // Shows a just-sent message immediately instead of waiting for it to
  // come back through the stream (see sendYouTubeChatMessage). Skips
  // adding it if the stream's echo already beat it here.
  const addSentMessage = useCallback((msg: YouTubeChatMessage) => {
    setMessages((prev) =>
      prev.some((m) => m.id === msg.id) ? prev : pushCapped(prev, msg),
    );
  }, []);

  return { messages, connected, ended, streamError, markDeleted, addSentMessage };
}
