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
  // Set right before this hook scrolls the container itself, so the
  // resulting native 'scroll' event isn't misread by onScroll as the
  // viewer scrolling away — see the comment in the effect below.
  const ignoreNextScroll = useRef(false);

  function onScroll() {
    const el = ref.current;
    if (!el) return;
    if (ignoreNextScroll.current) {
      ignoreNextScroll.current = false;
      return;
    }
    stickToBottom.current =
      el.scrollHeight - el.scrollTop - el.clientHeight <= BOTTOM_THRESHOLD_PX;
  }

  useEffect(() => {
    const el = ref.current;
    if (!el || !stickToBottom.current) return;

    const maxScrollTop = el.scrollHeight - el.clientHeight;
    if (el.scrollTop === maxScrollTop) return;

    // Browsers dispatch the 'scroll' event this assignment causes
    // asynchronously, and in a fast-moving chat more messages can
    // arrive — growing scrollHeight further — before it fires.
    // onScroll would then read a scrollHeight that's already grown
    // past what this assignment accounted for, look like the viewer
    // had scrolled away, and permanently stop auto-scrolling even
    // though nothing but this assignment happened. Ignoring the one
    // 'scroll' event this specific assignment triggers avoids that
    // race. (Only armed when the assignment actually moves scrollTop —
    // otherwise no event fires at all, and the flag would wrongly
    // swallow a later, real one.)
    ignoreNextScroll.current = true;
    el.scrollTop = el.scrollHeight;
  }, [dependency]);

  return { ref, onScroll };
}
