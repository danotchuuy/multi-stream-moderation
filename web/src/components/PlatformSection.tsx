import { useState, type ReactNode } from "react";

type Props = {
  label: string;
  children: ReactNode;
};

/** Wraps one platform's whole section — its connect screen, channel
 * picker, chat panel, and any companion panel rendered alongside it
 * (e.g. Kick's spam-groups panel) — behind a single minimize toggle,
 * so collapsing it hides everything for that provider at once, not
 * just the message log within an already-connected chat panel. */
export function PlatformSection({ label, children }: Props) {
  const [minimized, setMinimized] = useState(false);

  return (
    <section className="platform-panel">
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
