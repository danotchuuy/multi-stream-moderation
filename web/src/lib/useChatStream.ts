import { useCallback, useEffect, useState } from "react";
import type { ChatMessage } from "./api";
import type { ChatFragment } from "./chatDecorations";
import { clearNotifications, pushNotification } from "./notificationsStore";

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

/** A brand-new sub or a resub — mirrors subDTO in
 * internal/server/handlers_chat.go. months is how long the subscriber
 * has been subscribed: 1 for a first-time sub, or the resub's all-time
 * cumulative total otherwise. */
type SubEvent = {
  channel: string;
  displayName: string;
  tier: string;
  months: number;
  streakMonths?: number;
  isResub: boolean;
  /** Whatever the resubscriber optionally typed alongside their resub
   * notice; never set for a brand-new sub. */
  message?: string;
};

/** A chat message AutoMod is holding for review before it posts —
 * mirrors automodHoldDTO in internal/server/handlers_chat.go. */
export type AutomodHeldMessage = {
  messageId: string;
  username: string;
  displayName: string;
  userId: string;
  text: string;
  fragments?: ChatFragment[];
  /** "automod" or "blocked_term"; category/level only meaningful for
   * the former. */
  reason: string;
  category?: string;
  level?: number;
  timestamp: number;
};

/** A viewer's watch-streak milestone (consecutive streams watched) —
 * mirrors watchStreakDTO in internal/server/handlers_chat.go. Delivered
 * over a separate anonymous IRC connection, since Twitch's EventSub API
 * has no equivalent event for this (see twitch.ConnectWatchStreaks in
 * internal/twitch/watch_streaks.go) — that's invisible from here, it
 * just arrives as its own SSE event type. */
type WatchStreakEvent = {
  channel: string;
  login: string;
  displayName: string;
  streak: number;
};

/** A Bits cheer — mirrors cheerDTO in internal/server/handlers_chat.go.
 * DisplayName is already an anonymous placeholder when the cheer was
 * anonymous, so this needs no separate flag for that. */
type CheerEvent = {
  channel: string;
  displayName: string;
  bits: number;
  message?: string;
};

type AutomodHoldEvent = AutomodHeldMessage & { channel: string };

type AutomodUpdateEvent = {
  channel: string;
  messageId: string;
  /** "Approved", "Denied", or "Expired". */
  status: string;
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

function formatMonths(months: number): string {
  if (months >= 12) {
    const years = Math.floor(months / 12);
    const rem = months % 12;
    return rem === 0 ? `${years} year${years === 1 ? "" : "s"}` : `${years}y ${rem}m`;
  }
  return `${months} month${months === 1 ? "" : "s"}`;
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
  // Only ever populated for a viewer who's actually a moderator/the
  // broadcaster — Twitch itself only delivers these events to the
  // moderator who subscribed, so this stays empty for everyone else.
  const [automodQueue, setAutomodQueue] = useState<AutomodHeldMessage[]>([]);

  useEffect(() => {
    setMessages([]);
    setConnected(false);
    setAutomodQueue([]);
    clearNotifications("twitch");
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
      pushNotification({
        id: `giftsub-${Date.now()}-${gift.displayName}`,
        platform: "twitch",
        kind: "giftsub",
        channel: gift.channel,
        text: `${gift.displayName || "Someone"} gifted ${gift.count} sub${gift.count === 1 ? "" : "s"}!`,
        timestamp: Date.now(),
      });
    });

    source.addEventListener("sub", (event) => {
      const sub = JSON.parse((event as MessageEvent).data) as SubEvent;
      const name = sub.displayName || "Someone";
      const messageText = sub.message ? `: "${sub.message}"` : "";
      const text = sub.isResub
        ? `${name} resubscribed at ${sub.tier} — subscribed for ${formatMonths(sub.months)}${
            sub.streakMonths ? ` (${formatMonths(sub.streakMonths)} streak)` : ""
          }!${messageText}`
        : `${name} subscribed at ${sub.tier}!`;
      pushNotification({
        id: `sub-${Date.now()}-${sub.displayName}`,
        platform: "twitch",
        kind: "sub",
        channel: sub.channel,
        text,
        timestamp: Date.now(),
      });
    });

    source.addEventListener("watchstreak", (event) => {
      const streak = JSON.parse((event as MessageEvent).data) as WatchStreakEvent;
      const name = streak.displayName || streak.login || "A viewer";
      pushNotification({
        id: `watchstreak-${Date.now()}-${streak.login}`,
        platform: "twitch",
        kind: "watchstreak",
        channel: streak.channel,
        text: `${name} is on a ${streak.streak}-stream watch streak!`,
        timestamp: Date.now(),
      });
    });

    source.addEventListener("cheer", (event) => {
      const cheer = JSON.parse((event as MessageEvent).data) as CheerEvent;
      const messageText = cheer.message ? `: "${cheer.message}"` : "";
      pushNotification({
        id: `cheer-${Date.now()}-${cheer.displayName}`,
        platform: "twitch",
        kind: "cheer",
        channel: cheer.channel,
        text: `${cheer.displayName} cheered ${cheer.bits.toLocaleString()} bit${cheer.bits === 1 ? "" : "s"}${messageText}`,
        timestamp: Date.now(),
      });
    });

    source.addEventListener("automodhold", (event) => {
      const held = JSON.parse((event as MessageEvent).data) as AutomodHoldEvent;
      setAutomodQueue((prev) => [...prev, held]);
    });

    source.addEventListener("automodupdate", (event) => {
      const update = JSON.parse((event as MessageEvent).data) as AutomodUpdateEvent;
      setAutomodQueue((prev) => prev.filter((m) => m.messageId !== update.messageId));
    });

    return () => {
      source.close();
    };
  }, [channel]);

  // Optimistically drop a message from the queue as soon as the
  // caller's own allow/deny call succeeds, rather than waiting for the
  // automodupdate event that confirms it — that event still arrives
  // and is a no-op by then (the message is already gone), but this
  // keeps the queue feeling instant.
  const markAutomodResolved = useCallback((messageId: string) => {
    setAutomodQueue((prev) => prev.filter((m) => m.messageId !== messageId));
  }, []);

  return { messages, connected, automodQueue, markAutomodResolved };
}
