import { useEffect, useState } from "react";
import type { ChatMessage } from "./api";

const MAX_MESSAGES = 1000;

/** A chat row as rendered: either a real message (optionally struck
 * through after a delete/ban/timeout) or a synthetic system notice about
 * a ban/timeout/raid. */
export type DisplayMessage = ChatMessage & {
  deleted?: boolean;
  system?: boolean;
  /** Set on the synthetic notice for an incoming raid, so it can be
   * styled more prominently than an ordinary system notice. */
  raid?: boolean;
  /** Set on the synthetic notice for a gift sub / community gift, so it
   * can be styled distinctly from an ordinary system notice. */
  giftSub?: boolean;
  /** Monotonically increasing arrival order, stamped by pushCapped.
   * Used (instead of a message's position in the array) for the chat
   * log's alternating row background — the array's front gets trimmed
   * once MAX_MESSAGES is hit, which would otherwise shift every
   * surviving row's index — and so its :nth-child parity — by one on
   * every single new message, flickering the whole log's stripes. seq
   * is assigned once per message and never changes, so parity stays
   * stable regardless of how the window slides. */
  seq: number;
};

type RaidEvent = {
  channel: string;
  displayName: string;
  login: string;
  viewerCount: number;
};

/** A single gift or a batch gifted at once — just the gifter and how
 * many, never who each one went to (see giftSubDTO in
 * internal/server/handlers_chat.go). */
type GiftSubEvent = {
  channel: string;
  displayName: string;
  count: number;
};

type ClearMsgEvent = {
  channel: string;
  messageId: string;
};

type ClearChatEvent = {
  channel: string;
  userId?: string;
  username?: string;
  durationSeconds?: number;
};

function pushCapped(
  list: DisplayMessage[],
  next: Omit<DisplayMessage, "seq">,
): DisplayMessage[] {
  const seq = (list[list.length - 1]?.seq ?? 0) + 1;
  const trimmed = list.length >= MAX_MESSAGES ? list.slice(1) : list;
  return [...trimmed, { ...next, seq }];
}

function formatDuration(seconds: number): string {
  if (seconds >= 3600) return `${Math.round(seconds / 3600)}h`;
  if (seconds >= 60) return `${Math.round(seconds / 60)}m`;
  return `${seconds}s`;
}

/** useChatStream opens a server-sent-events connection to
 * /api/chat/stream for `channel` and keeps the most recent messages,
 * reflecting moderation actions (message deletion, bans/timeouts) as
 * they come in from Twitch — whether triggered from this UI or
 * elsewhere. Passing a different channel (or null) tears down the
 * previous connection and starts fresh. */
export function useChatStream(channel: string | null) {
  const [messages, setMessages] = useState<DisplayMessage[]>([]);
  const [connected, setConnected] = useState(false);

  useEffect(() => {
    setMessages([]);
    setConnected(false);
    if (!channel) {
      return;
    }

    const source = new EventSource(
      `/api/chat/stream?channel=${encodeURIComponent(channel)}`,
    );
    source.onopen = () => setConnected(true);
    source.onerror = () => setConnected(false);

    source.addEventListener("message", (event) => {
      const msg = JSON.parse((event as MessageEvent).data) as ChatMessage;
      setMessages((prev) => pushCapped(prev, msg));
    });

    source.addEventListener("clearmsg", (event) => {
      const clear = JSON.parse((event as MessageEvent).data) as ClearMsgEvent;
      setMessages((prev) =>
        prev.map((m) => (m.id === clear.messageId ? { ...m, deleted: true } : m)),
      );
    });

    source.addEventListener("clearchat", (event) => {
      const clear = JSON.parse((event as MessageEvent).data) as ClearChatEvent;
      setMessages((prev) => {
        if (!clear.userId) {
          // A full-chat clear (e.g. someone ran /clear) rather than a
          // single user's ban/timeout.
          return [];
        }

        const withDeletions = prev.map((m) =>
          m.userId === clear.userId ? { ...m, deleted: true } : m,
        );

        const notice: Omit<DisplayMessage, "seq"> = {
          id: `notice-${Date.now()}-${clear.userId}`,
          channel: clear.channel,
          // Twitch's EventSub replacement for this event doesn't say
          // whether it was a ban or a timeout, or for how long — only
          // that the user's messages were cleared — so this can only be
          // worded generically unless durationSeconds happens to be set.
          text: clear.durationSeconds
            ? `${clear.username || "a chatter"} was timed out for ${formatDuration(clear.durationSeconds)}`
            : `${clear.username || "a chatter"} was removed from chat`,
          username: "",
          displayName: "",
          userId: "",
          color: "",
          mod: false,
          subscriber: false,
          broadcaster: false,
          badges: [],
          timestamp: Date.now(),
          system: true,
        };
        return pushCapped(withDeletions, notice);
      });
    });

    source.addEventListener("raid", (event) => {
      const raid = JSON.parse((event as MessageEvent).data) as RaidEvent;
      const notice: Omit<DisplayMessage, "seq"> = {
        id: `raid-${Date.now()}-${raid.login}`,
        channel: raid.channel,
        text: `${raid.displayName || "a channel"} is raiding with ${raid.viewerCount.toLocaleString()} viewer${raid.viewerCount === 1 ? "" : "s"}!`,
        username: "",
        displayName: "",
        userId: "",
        color: "",
        mod: false,
        subscriber: false,
        broadcaster: false,
        badges: [],
        timestamp: Date.now(),
        system: true,
        raid: true,
      };
      setMessages((prev) => pushCapped(prev, notice));
    });

    source.addEventListener("giftsub", (event) => {
      const gift = JSON.parse((event as MessageEvent).data) as GiftSubEvent;
      const notice: Omit<DisplayMessage, "seq"> = {
        id: `giftsub-${Date.now()}-${gift.displayName}`,
        channel: gift.channel,
        text: `${gift.displayName || "Someone"} gifted ${gift.count} sub${gift.count === 1 ? "" : "s"}!`,
        username: "",
        displayName: "",
        userId: "",
        color: "",
        mod: false,
        subscriber: false,
        broadcaster: false,
        badges: [],
        timestamp: Date.now(),
        system: true,
        giftSub: true,
      };
      setMessages((prev) => pushCapped(prev, notice));
    });

    return () => {
      source.close();
    };
  }, [channel]);

  return { messages, connected };
}
