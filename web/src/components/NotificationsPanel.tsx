import { useAutoScroll } from "../lib/useAutoScroll";
import { useNotifications, type PlatformNotification } from "../lib/notificationsStore";
import { PlatformSection } from "./PlatformSection";

const KIND_EMOJI: Record<PlatformNotification["kind"], string> = {
  sub: "⭐",
  giftsub: "🎁",
  watchstreak: "🔥",
  kicksgifted: "🪙",
  cheer: "💎",
};

// Reuses the exact classes each platform's own chat log already styles
// these notice kinds with (see App.css) — "giftsub" from Twitch and
// Kick share a look, "sub"/"watchstreak"/"cheer" are Twitch-only,
// "kicksgifted" Kick-only.
const KIND_CLASS: Record<PlatformNotification["kind"], string> = {
  sub: "chat-sub",
  giftsub: "chat-giftsub",
  watchstreak: "chat-watchstreak",
  kicksgifted: "chat-kicksgifted",
  cheer: "chat-cheer",
};

const PLATFORM_LABEL: Record<PlatformNotification["platform"], string> = {
  twitch: "Twitch",
  kick: "Kick",
};

/** NotificationsPanel is the combined feed for every platform's
 * "money/support" events — subs, gift subs, watch streaks, Kicks
 * spent, and Bits cheers today; Super Chats will land here too once
 * that's implemented (see PlatformNotification) — pulled out of each
 * platform's own chat log into one shared window so they're never lost
 * in the scroll of ordinary messages. */
export function NotificationsPanel() {
  const notifications = useNotifications();
  const { ref, onScroll } = useAutoScroll(notifications);

  return (
    <PlatformSection label="Notifications" className="notifications-panel">
      <div className="card chat-panel">
        <ul className="chat-log" ref={ref} onScroll={onScroll}>
          {notifications.map((n) => (
            <li key={n.id} className={`chat-system ${KIND_CLASS[n.kind]}`}>
              <span className="chat-text">
                <span className={`platform-badge ${n.platform}-badge`}>
                  {PLATFORM_LABEL[n.platform]}
                </span>{" "}
                {KIND_EMOJI[n.kind]} {n.text}
              </span>
            </li>
          ))}
          {notifications.length === 0 && (
            <li className="chat-empty">No notifications yet.</li>
          )}
        </ul>
      </div>
    </PlatformSection>
  );
}
