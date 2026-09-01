import { useCallback, useEffect, useState } from "react";
import type { KickChatMessage } from "./kickApi";

const MAX_MESSAGES = 200;

export type KickDisplayMessage = KickChatMessage & {
  deleted?: boolean;
  system?: boolean;
};

type KickBannedEvent = {
  channel: string;
  userId: number;
  username: string;
  durationMinutes?: number;
};

function pushCapped(
  list: KickDisplayMessage[],
  next: KickDisplayMessage,
): KickDisplayMessage[] {
  const trimmed = list.length >= MAX_MESSAGES ? list.slice(1) : list;
  return [...trimmed, next];
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
        const notice: KickDisplayMessage = {
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
