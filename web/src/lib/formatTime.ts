/** Formats a chat message's arrival time as a locale-aware "hh:mm" for
 * display beside the message — never the date, since the log only ever
 * shows recent history. */
export function formatClockTime(timestamp: number): string {
  return new Date(timestamp).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
  });
}
