import { useEffect, useState, type ReactNode } from "react";

type Props = {
  label: string;
  children: ReactNode;
  /** Extra class(es) appended to the section's own "platform-panel", so
   * a particular section can tweak layout (width, ordering, ...)
   * without every other caller having to know about it. */
  className?: string;
};

const MINIMIZED_STORAGE_PREFIX = "platformPanel.minimized.";

/** Reads whether label's panel was left minimized, from a previous
 * visit — false (i.e. treat storage as absent) for any reason it can't
 * be read, e.g. private browsing with storage disabled. */
function readMinimized(label: string): boolean {
  try {
    return localStorage.getItem(MINIMIZED_STORAGE_PREFIX + label) === "1";
  } catch {
    return false;
  }
}

function writeMinimized(label: string, minimized: boolean) {
  try {
    if (minimized) {
      localStorage.setItem(MINIMIZED_STORAGE_PREFIX + label, "1");
    } else {
      localStorage.removeItem(MINIMIZED_STORAGE_PREFIX + label);
    }
  } catch {
    // Storage unavailable — the toggle still works for this session, it
    // just won't be remembered next time.
  }
}

/** Wraps one platform's whole section — its connect screen, channel
 * picker, chat panel, and any companion panel rendered alongside it
 * (e.g. Kick's spam-groups panel) — behind a single minimize toggle,
 * so collapsing it hides everything for that provider at once, not
 * just the message log within an already-connected chat panel.
 * Whether it's minimized is remembered (per label, e.g. "Twitch") in
 * localStorage, so it stays collapsed across page reloads and future
 * visits until expanded again. */
export function PlatformSection({ label, children, className }: Props) {
  const [minimized, setMinimized] = useState(() => readMinimized(label));

  useEffect(() => {
    writeMinimized(label, minimized);
  }, [label, minimized]);

  return (
    <section
      className={["platform-panel", className, minimized && "platform-panel-minimized"]
        .filter(Boolean)
        .join(" ")}
    >
      <div className="platform-panel-header">
        <span className="platform-panel-label">{label}</span>
        <button
          type="button"
          className="link-button"
          aria-label={minimized ? `Expand ${label}` : `Minimize ${label}`}
          title={minimized ? `Expand ${label}` : `Minimize ${label}`}
          onClick={() => setMinimized((m) => !m)}
        >
          {minimized ? "▸" : "▾"}
        </button>
      </div>
      {!minimized && children}
    </section>
  );
}
