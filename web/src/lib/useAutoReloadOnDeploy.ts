import { useEffect } from "react";

type HealthResponse = {
  status: string;
  version: string;
};

// Cheap enough (a tiny same-origin JSON GET) to run unconditionally on
// this interval for as long as the tab is open, rather than only while
// some other part of the page happens to be disconnected.
const POLL_INTERVAL_MS = 30_000;

/** Resolves to the server's current instanceID (see Server.instanceID
 * in internal/server/server.go), or null if the request failed — the
 * server being briefly unreachable (e.g. mid-restart during a deploy)
 * is expected and not itself treated as a version change. */
async function fetchHealthVersion(): Promise<string | null> {
  try {
    const res = await fetch("/api/health");
    if (!res.ok) return null;
    const data: HealthResponse = await res.json();
    return data.version || null;
  } catch {
    return null;
  }
}

/** Reloads the page the first time it notices the server's running
 * process has changed since this tab loaded — i.e. a deploy happened
 * while the tab was open — rather than leaving the tab running its now
 * stale JS bundle indefinitely even once its chat streams reconnect to
 * the new process on their own (EventSource already retries those on
 * its own; this only handles picking up the new frontend build).
 * Polling /api/health directly (rather than reacting to a chat stream's
 * connected state) means this still catches a redeploy that completes
 * quickly enough that no stream's connection ever visibly dropped. */
export function useAutoReloadOnDeploy() {
  useEffect(() => {
    let baseline: string | null = null;
    let cancelled = false;

    async function tick() {
      const version = await fetchHealthVersion();
      if (cancelled || version === null) return;
      if (baseline === null) {
        baseline = version;
        return;
      }
      if (version !== baseline) {
        window.location.reload();
      }
    }

    tick();
    const interval = setInterval(tick, POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, []);
}
