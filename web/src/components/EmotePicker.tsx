import { useEffect, useMemo, useRef, useState } from "react";
import type { ChatEmote } from "../lib/chatDecorations";

type Props = {
  /** Loads the channel's available emotes; called once, the first time
   * the picker is opened, and cached for the life of this component. */
  fetchEmotes: () => Promise<ChatEmote[]>;
  onSelect: (emote: ChatEmote) => void;
};

// Maps a raw platform emote type (Twitch's emote_type, or Kick's
// synthesized "channel"/"global"/"emoji") to the label shown above its
// group in the picker.
const TYPE_LABELS: Record<string, string> = {
  subscriptions: "Subscriber",
  follower: "Follower",
  globals: "Global",
  smilies: "Global",
  prime: "Prime",
  turbo: "Turbo",
  channel: "Channel",
  global: "Global",
  emoji: "Kick Emoji",
};

// Puts the emotes most relevant to *this* channel first (subscriber,
// follower, the channel's own set), shared platform-wide ones after.
const TYPE_ORDER = [
  "subscriptions",
  "follower",
  "channel",
  "prime",
  "turbo",
  "globals",
  "global",
  "smilies",
  "emoji",
];

function emoteTypeLabel(type: string): string {
  if (!type) return "Emotes";
  return TYPE_LABELS[type] ?? type.charAt(0).toUpperCase() + type.slice(1);
}

/** A popover of the viewer's actual Twitch/Kick emotes for the channel
 * they're chatting in, opened from a toggle button next to the chat
 * composer's text input. Selecting one inserts its name into the
 * message via onSelect; the panel stays open so several can be picked
 * in a row, and closes on outside click, Escape, or re-toggling. */
export function EmotePicker({ fetchEmotes, onSelect }: Props) {
  const [open, setOpen] = useState(false);
  const [emotes, setEmotes] = useState<ChatEmote[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState("");
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;

    const handleClick = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", handleClick);
    document.addEventListener("keydown", handleKey);
    return () => {
      document.removeEventListener("mousedown", handleClick);
      document.removeEventListener("keydown", handleKey);
    };
  }, [open]);

  useEffect(() => {
    if (!open || emotes !== null || loading) return;
    setLoading(true);
    setError(null);
    fetchEmotes()
      .then(setEmotes)
      .catch((err) => setError(err instanceof Error ? err.message : "failed to load emotes"))
      .finally(() => setLoading(false));
  }, [open, emotes, loading, fetchEmotes]);

  const groups = useMemo(() => {
    const term = filter.trim().toLowerCase();
    const filtered = (emotes ?? []).filter(
      (e) => !term || e.name.toLowerCase().includes(term),
    );
    const byType = new Map<string, ChatEmote[]>();
    for (const emote of filtered) {
      const key = emote.type ?? "";
      if (!byType.has(key)) byType.set(key, []);
      byType.get(key)!.push(emote);
    }
    return [...byType.entries()].sort(([a], [b]) => {
      const ai = TYPE_ORDER.indexOf(a);
      const bi = TYPE_ORDER.indexOf(b);
      if (ai === -1 && bi === -1) return a.localeCompare(b);
      if (ai === -1) return 1;
      if (bi === -1) return -1;
      return ai - bi;
    });
  }, [emotes, filter]);

  return (
    <div className="emote-picker" ref={rootRef}>
      <button
        type="button"
        className="emote-picker-toggle"
        aria-label="Insert emote"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        😊
      </button>
      {open && (
        <div className="emote-picker-panel" role="menu">
          <input
            type="text"
            className="emote-picker-search"
            placeholder="Search emotes..."
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
          {loading && <p className="emote-picker-status">Loading emotes...</p>}
          {error && <p className="emote-picker-status emote-picker-error">{error}</p>}
          {!loading && !error && groups.length === 0 && (
            <p className="emote-picker-status">No emotes found.</p>
          )}
          {groups.map(([type, list]) => (
            <div key={type || "untyped"} className="emote-picker-category">
              <p className="emote-picker-category-label">{emoteTypeLabel(type)}</p>
              <div className="emote-picker-grid">
                {list.map((emote) => (
                  <button
                    key={emote.id}
                    type="button"
                    className="emote-picker-item"
                    title={emote.name + (emote.locked ? " (subscribers only)" : "")}
                    onClick={() => onSelect(emote)}
                  >
                    <img src={emote.imageUrl} alt={emote.name} loading="lazy" />
                    {emote.locked && <span className="emote-picker-lock">🔒</span>}
                  </button>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
