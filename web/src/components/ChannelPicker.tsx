import { useState } from "react";
import type { ModeratedChannel, TwitchUser } from "../lib/api";

type Props = {
  user: TwitchUser;
  channels: ModeratedChannel[] | null;
  loadError?: string | null;
  onSelect: (channel: string) => void;
  onSignOut: () => void;
};

export function ChannelPicker({
  user,
  channels,
  loadError,
  onSelect,
  onSignOut,
}: Props) {
  const [manualChannel, setManualChannel] = useState("");

  return (
    <div className="card">
      <header className="picker-header">
        <img src={user.profileImageUrl} alt="" className="avatar" />
        <div>
          <p>
            Signed in as <strong>{user.displayName}</strong>
          </p>
          <button className="link-button" onClick={onSignOut}>
            Sign out
          </button>
        </div>
      </header>

      <h2>Choose a channel</h2>

      {channels === null && !loadError && (
        <p>Loading your moderated channels...</p>
      )}
      {loadError && <p className="error">{loadError}</p>}

      {channels && channels.length > 0 && (
        <ul className="channel-list">
          {channels.map((c) => (
            <li key={c.broadcasterId}>
              <button
                className="channel-button"
                onClick={() => onSelect(c.broadcasterLogin)}
              >
                {c.broadcasterName}
              </button>
            </li>
          ))}
        </ul>
      )}
      {channels && channels.length === 0 && (
        <p>You aren't a moderator on any channels yet.</p>
      )}

      <form
        className="manual-channel"
        onSubmit={(e) => {
          e.preventDefault();
          const name = manualChannel.trim();
          if (name) onSelect(name);
        }}
      >
        <label htmlFor="channel-input">Or watch any channel by name</label>
        <div className="manual-channel-row">
          <input
            id="channel-input"
            value={manualChannel}
            onChange={(e) => setManualChannel(e.target.value)}
            placeholder="channel name"
            autoComplete="off"
          />
          <button type="submit" disabled={!manualChannel.trim()}>
            Watch
          </button>
        </div>
      </form>
    </div>
  );
}
