import { useEffect, useState } from "react";
import { useAutoScroll } from "../lib/useAutoScroll";
import { formatClockTime } from "../lib/formatTime";
import { setShowTimestamps, useShowTimestamps } from "../lib/settingsStore";
import {
  useNotifications,
  type PlatformNotification,
  type PlatformNotificationKind,
} from "../lib/notificationsStore";
import { PlatformSection } from "./PlatformSection";

const KIND_EMOJI: Record<PlatformNotification["kind"], string> = {
  sub: "⭐",
  giftsub: "🎁",
  watchstreak: "🔥",
  kicksgifted: "🪙",
  cheer: "💎",
  raid: "🎉",
};

// Reuses the exact classes each platform's own chat log already styles
// these notice kinds with (see App.css) — "giftsub" from Twitch and
// Kick share a look, "sub"/"watchstreak"/"cheer"/"raid" are Twitch-only,
// "kicksgifted" Kick-only.
const KIND_CLASS: Record<PlatformNotification["kind"], string> = {
  sub: "chat-sub",
  giftsub: "chat-giftsub",
  watchstreak: "chat-watchstreak",
  kicksgifted: "chat-kicksgifted",
  cheer: "chat-cheer",
  raid: "chat-raid",
};

const PLATFORM_LABEL: Record<PlatformNotification["platform"], string> = {
  twitch: "Twitch",
  kick: "Kick",
};

const KIND_LABEL: Record<PlatformNotificationKind, string> = {
  sub: "Subs",
  giftsub: "Gift subs",
  watchstreak: "Streaks",
  kicksgifted: "Kicks gifted",
  cheer: "Cheers",
  raid: "Raids",
};

const ALL_KINDS = Object.keys(KIND_LABEL) as PlatformNotificationKind[];

const FILTER_STORAGE_KEY = "notificationsPanel.visibleKinds";

/** Reads which notification kinds were left checked from a previous
 * visit — every kind (i.e. treat storage as absent) for any reason it
 * can't be read, e.g. private browsing with storage disabled, or a
 * kind that's been added since the saved list was written. */
function readVisibleKinds(): Set<PlatformNotificationKind> {
  try {
    const raw = localStorage.getItem(FILTER_STORAGE_KEY);
    if (!raw) return new Set(ALL_KINDS);
    const saved: unknown = JSON.parse(raw);
    if (!Array.isArray(saved)) return new Set(ALL_KINDS);
    return new Set(ALL_KINDS.filter((kind) => saved.includes(kind)));
  } catch {
    return new Set(ALL_KINDS);
  }
}

function writeVisibleKinds(kinds: Set<PlatformNotificationKind>) {
  try {
    localStorage.setItem(FILTER_STORAGE_KEY, JSON.stringify([...kinds]));
  } catch {
    // Storage unavailable — the filter still works for this session, it
    // just won't be remembered next time.
  }
}

/** NotificationsPanel is the combined feed for every platform's
 * "money/support" and raid events — subs, gift subs, watch streaks,
 * Kicks spent, Bits cheers, and incoming raids today; Super Chats will
 * land here too once that's implemented (see PlatformNotification) —
 * pulled out of each platform's own chat log into one shared window so
 * they're never lost in the scroll of ordinary messages. A row of
 * checkboxes above the list lets the viewer hide kinds they don't care
 * about; the choice is remembered in localStorage across visits. A
 * further checkbox there toggles timestamps — shared with every chat
 * panel (see settingsStore) rather than just this list, since it's the
 * one settings-like control in the app. */
export function NotificationsPanel() {
  const notifications = useNotifications();
  const [visibleKinds, setVisibleKinds] = useState(readVisibleKinds);
  const showTimestamps = useShowTimestamps();

  useEffect(() => {
    writeVisibleKinds(visibleKinds);
  }, [visibleKinds]);

  function toggleKind(kind: PlatformNotificationKind) {
    setVisibleKinds((prev) => {
      const next = new Set(prev);
      if (next.has(kind)) {
        next.delete(kind);
      } else {
        next.add(kind);
      }
      return next;
    });
  }

  const filtered = notifications.filter((n) => visibleKinds.has(n.kind));
  const { ref, onScroll } = useAutoScroll(filtered);

  return (
    <PlatformSection label="Notifications" className="notifications-panel">
      <div className="card chat-panel">
        <div className="notifications-filters">
          {ALL_KINDS.map((kind) => (
            <label key={kind} className="notifications-filter">
              <input
                type="checkbox"
                checked={visibleKinds.has(kind)}
                onChange={() => toggleKind(kind)}
              />
              {KIND_EMOJI[kind]} {KIND_LABEL[kind]}
            </label>
          ))}
        </div>
        <div className="notifications-filters notifications-settings">
          <label className="notifications-filter">
            <input
              type="checkbox"
              checked={showTimestamps}
              onChange={(e) => setShowTimestamps(e.target.checked)}
            />
            Show timestamps
          </label>
        </div>
        <ul className="chat-log" ref={ref} onScroll={onScroll}>
          {filtered.map((n) => (
            <li key={n.id} className={`chat-system ${KIND_CLASS[n.kind]}`}>
              {showTimestamps && (
                <span className="chat-timestamp">{formatClockTime(n.timestamp)}</span>
              )}
              <span className="chat-text">
                <span className={`platform-badge ${n.platform}-badge`}>
                  {PLATFORM_LABEL[n.platform]}
                </span>{" "}
                {KIND_EMOJI[n.kind]} {n.text}
              </span>
            </li>
          ))}
          {filtered.length === 0 && (
            <li className="chat-empty">
              {notifications.length === 0
                ? "No notifications yet."
                : "No notifications match the selected filters."}
            </li>
          )}
        </ul>
      </div>
    </PlatformSection>
  );
}
