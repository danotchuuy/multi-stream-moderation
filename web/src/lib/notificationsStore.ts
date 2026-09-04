import { useSyncExternalStore } from "react";

const MAX_NOTIFICATIONS = 200;

export type PlatformNotificationKind =
  | "sub"
  | "giftsub"
  | "watchstreak"
  | "kicksgifted"
  | "cheer";

/** One "money/support" event — a sub, gift, watch streak, Kicks spend,
 * or Bits cheer — from any connected platform, combined into a single
 * feed
 * (see NotificationsPanel) instead of interleaved into that platform's
 * own chat log. Platform-specific hooks (useChatStream,
 * useKickChatStream) format `text` themselves and hand it to
 * pushNotification; this module just fans it out to whoever's
 * subscribed. */
export type PlatformNotification = {
  id: string;
  platform: "twitch" | "kick";
  kind: PlatformNotificationKind;
  channel: string;
  text: string;
  timestamp: number;
};

let notifications: PlatformNotification[] = [];
const listeners = new Set<() => void>();

function emit() {
  for (const listener of listeners) listener();
}

export function pushNotification(notification: PlatformNotification) {
  notifications =
    notifications.length >= MAX_NOTIFICATIONS
      ? [...notifications.slice(1), notification]
      : [...notifications, notification];
  emit();
}

/** Clears every queued notification for `platform` — call this when
 * that platform's chat connection resets (channel changed, stream
 * torn down), so a combined feed spanning multiple platforms doesn't
 * keep showing entries from a channel that's no longer open. */
export function clearNotifications(platform: PlatformNotification["platform"]) {
  notifications = notifications.filter((n) => n.platform !== platform);
  emit();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

function getSnapshot() {
  return notifications;
}

/** useNotifications subscribes a component to the combined
 * notification feed across every connected platform. */
export function useNotifications(): PlatformNotification[] {
  return useSyncExternalStore(subscribe, getSnapshot);
}
