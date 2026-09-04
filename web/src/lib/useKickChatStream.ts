import { useCallback, useEffect, useState } from "react";
import type { KickChatMessage } from "./kickApi";
import { clearNotifications, pushNotification } from "./notificationsStore";

const MAX_MESSAGES = 1000;

export type KickDisplayMessage = KickChatMessage & {
  deleted?: boolean;
  system?: boolean;
  /** Monotonically increasing arrival order, stamped by pushCapped —
   * see the matching field on DisplayMessage in useChatStream.ts for
   * why this exists instead of using a message's position in the
   * array for the chat log's alternating row background. */
  seq: number;
};

type KickBannedEvent = {
  channel: string;
  userId: number;
  username: string;
  durationMinutes?: number;
};

/** A single gift or a batch gifted at once — just the gifter and how
 * many, never who each one went to (see giftSubDTO in
 * internal/server/handlers_chat.go). */
type GiftSubEvent = {
  channel: string;
  displayName: string;
  count: number;
};

/** Kicks (Kick's virtual currency) spent in the channel — mirrors
 * kicksGiftedDTO in internal/server/handlers_kick.go. */
type KicksGiftedEvent = {
  channel: string;
  displayName: string;
  amount: number;
  giftName?: string;
  message?: string;
};

function pushCapped(
  list: KickDisplayMessage[],
  next: Omit<KickDisplayMessage, "seq">,
): KickDisplayMessage[] {
  const seq = (list[list.length - 1]?.seq ?? 0) + 1;
  const trimmed = list.length >= MAX_MESSAGES ? list.slice(1) : list;
  return [...trimmed, { ...next, seq }];
}

function formatMinutes(minutes: number): string {
  if (minutes >= 1440) return `${Math.round(minutes / 1440)}d`;
  if (minutes >= 60) return `${Math.round(minutes / 60)}h`;
  return `${minutes}m`;
}

/** useKickChatStream opens a server-sent-events connection to
 * /api/kick/chat/stream for `channel`. Kick has no server-pushed event
 * for a single message being deleted (unlike Twitch's CLEARMSG), so
 * `markDeleted` lets the caller reflect its own successful delete calls
 * optimistically; a ban/timeout still arrives as a real "banned" event
 * from Kick's webhook and is reflected for every viewer. */
export function useKickChatStream(channel: string | null) {
  const [messages, setMessages] = useState<KickDisplayMessage[]>([]);
  const [connected, setConnected] = useState(false);

  useEffect(() => {
    setMessages([]);
    setConnected(false);
    clearNotifications("kick");
    if (!channel) {
      return;
    }

    const source = new EventSource(
      `/api/kick/chat/stream?channel=${encodeURIComponent(channel)}`,
    );
    source.onopen = () => setConnected(true);
    source.onerror = () => setConnected(false);

    source.addEventListener("message", (event) => {
      const msg = JSON.parse((event as MessageEvent).data) as KickChatMessage;
      setMessages((prev) => pushCapped(prev, msg));
    });

    source.addEventListener("banned", (event) => {
      const banned = JSON.parse((event as MessageEvent).data) as KickBannedEvent;
      setMessages((prev) => {
        const withDeletions = prev.map((m) =>
          m.userId === banned.userId ? { ...m, deleted: true } : m,
        );
        const notice: Omit<KickDisplayMessage, "seq"> = {
          id: `notice-${Date.now()}-${banned.userId}`,
          channel: banned.channel,
          text: banned.durationMinutes
            ? `${banned.username || "a chatter"} was timed out for ${formatMinutes(banned.durationMinutes)}`
            : `${banned.username || "a chatter"} was banned`,
          username: "",
          userId: 0,
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

    source.addEventListener("giftsub", (event) => {
      const gift = JSON.parse((event as MessageEvent).data) as GiftSubEvent;
      pushNotification({
        id: `giftsub-${Date.now()}-${gift.displayName}`,
        platform: "kick",
        kind: "giftsub",
        channel: gift.channel,
        text: `${gift.displayName || "Someone"} gifted ${gift.count} sub${gift.count === 1 ? "" : "s"}!`,
        timestamp: Date.now(),
      });
    });

    source.addEventListener("kicksgifted", (event) => {
      const kicks = JSON.parse((event as MessageEvent).data) as KicksGiftedEvent;
      const amountText = `${kicks.amount.toLocaleString()} Kick${kicks.amount === 1 ? "" : "s"}`;
      const giftText = kicks.giftName ? ` (${kicks.giftName})` : "";
      const messageText = kicks.message ? `: "${kicks.message}"` : "";
      pushNotification({
        id: `kicksgifted-${Date.now()}-${kicks.displayName}`,
        platform: "kick",
        kind: "kicksgifted",
        channel: kicks.channel,
        text: `${kicks.displayName || "Someone"} spent ${amountText}${giftText}${messageText}`,
        timestamp: Date.now(),
      });
    });

    return () => {
      source.close();
    };
  }, [channel]);

  const markDeleted = useCallback((messageId: string) => {
    setMessages((prev) =>
      prev.map((m) => (m.id === messageId ? { ...m, deleted: true } : m)),
    );
  }, []);

  return { messages, connected, markDeleted };
}
