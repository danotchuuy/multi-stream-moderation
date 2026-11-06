import { useSyncExternalStore } from "react";

const SHOW_TIMESTAMPS_KEY = "settings.showTimestamps";

function readShowTimestamps(): boolean {
  try {
    return localStorage.getItem(SHOW_TIMESTAMPS_KEY) !== "0";
  } catch {
    return true;
  }
}

let showTimestamps = readShowTimestamps();
const listeners = new Set<() => void>();

function emit() {
  for (const listener of listeners) listener();
}

export function setShowTimestamps(value: boolean) {
  showTimestamps = value;
  try {
    localStorage.setItem(SHOW_TIMESTAMPS_KEY, value ? "1" : "0");
  } catch {
    // Storage unavailable — the setting still applies for this session,
    // it just won't be remembered next time.
  }
  emit();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

function getSnapshot() {
  return showTimestamps;
}

/** useShowTimestamps subscribes a component to the single "show
 * timestamps" setting shared by every chat panel and the notifications
 * feed — toggled from one checkbox in NotificationsPanel rather than
 * per panel, since there's no reason a viewer would want it on for one
 * platform's chat but not another's. */
export function useShowTimestamps(): boolean {
  return useSyncExternalStore(subscribe, getSnapshot);
}
