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
  superchat: "💵",
  membership: "⭐",
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
  superchat: "chat-superchat",
  membership: "chat-sub",
};

const PLATFORM_LABEL: Record<PlatformNotification["platform"], string> = {
  twitch: "Twitch",
  kick: "Kick",
  youtube: "YouTube",
};

const KIND_LABEL: Record<PlatformNotificationKind, string> = {
  sub: "Subs",
  giftsub: "Gift subs",
  watchstreak: "Streaks",
  kicksgifted: "Kicks gifted",
  cheer: "Cheers",
  raid: "Raids",
  superchat: "Super Chats",
  membership: "Memberships",
};

const ALL_KINDS = Object.keys(KIND_LABEL) as PlatformNotificationKind[];

/** Stores the kinds the viewer has *hidden*, so a kind added later
 * (like Super Chats) starts out visible instead of silently filtered. */
const HIDDEN_STORAGE_KEY = "notificationsPanel.hiddenKinds";
/** The previous format: the kinds left *visible*, written before any
 * YouTube kinds existed. Read once to carry an existing filter over. */
const LEGACY_VISIBLE_STORAGE_KEY = "notificationsPanel.visibleKinds";
const LEGACY_KINDS: PlatformNotificationKind[] = [
  "sub",
  "giftsub",
  "watchstreak",
  "kicksgifted",
  "cheer",
  "raid",
];

function parseKindList(raw: string | null): PlatformNotificationKind[] | null {
  if (!raw) return null;
  const saved: unknown = JSON.parse(raw);
  if (!Array.isArray(saved)) return null;
  return ALL_KINDS.filter((kind) => saved.includes(kind));
}

/** Reads which notification kinds were left checked from a previous
 * visit — every kind for any reason storage can't be read, e.g.
 * private browsing with storage disabled. */
function readVisibleKinds(): Set<PlatformNotificationKind> {
  try {
    const hidden = parseKindList(localStorage.getItem(HIDDEN_STORAGE_KEY));
    if (hidden) return new Set(ALL_KINDS.filter((kind) => !hidden.includes(kind)));

    const legacyVisible = parseKindList(localStorage.getItem(LEGACY_VISIBLE_STORAGE_KEY));
    if (legacyVisible) {
      return new Set(
        ALL_KINDS.filter((kind) => !LEGACY_KINDS.includes(kind) || legacyVisible.includes(kind)),
      );
    }
  } catch {
    // Fall through to showing everything.
  }
  return new Set(ALL_KINDS);
}

function writeVisibleKinds(kinds: Set<PlatformNotificationKind>) {
  try {
    const hidden = ALL_KINDS.filter((kind) => !kinds.has(kind));
    localStorage.setItem(HIDDEN_STORAGE_KEY, JSON.stringify(hidden));
    localStorage.removeItem(LEGACY_VISIBLE_STORAGE_KEY);
  } catch {
    // Storage unavailable — the filter still works for this session, it
    // just won't be remembered next time.
  }
}

/** NotificationsPanel is the combined feed for every platform's
 * "money/support" and raid events — subs, gift subs, watch streaks,
 * Kicks spent, Bits cheers, YouTube Super Chats and memberships, and
 * incoming raids (see PlatformNotification) —
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
