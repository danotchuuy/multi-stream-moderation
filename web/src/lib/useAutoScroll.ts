import { useEffect, useRef } from "react";

/** How close to the bottom (in pixels) counts as "still at the
 * bottom" — a little slack so sub-pixel scroll rounding doesn't count
 * as the viewer having scrolled away. */
const BOTTOM_THRESHOLD_PX = 48;

/** useAutoScroll keeps a scrollable element pinned to its bottom as
 * `dependency` changes (typically a chat panel's message list), so new
 * messages stay visible without the viewer having to scroll down
 * themselves. If the viewer scrolls up to read earlier messages, new
 * messages stop yanking the view back down until they scroll back to
 * the bottom on their own. */
export function useAutoScroll<T>(dependency: T) {
  const ref = useRef<HTMLUListElement | null>(null);
  const stickToBottom = useRef(true);

  function onScroll() {
    const el = ref.current;
    if (!el) return;
    stickToBottom.current =
      el.scrollHeight - el.scrollTop - el.clientHeight <= BOTTOM_THRESHOLD_PX;
  }

  useEffect(() => {
    const el = ref.current;
    if (!el || !stickToBottom.current) return;
    el.scrollTop = el.scrollHeight;
  }, [dependency]);

  return { ref, onScroll };
}
