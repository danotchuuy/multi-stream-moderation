import { useEffect, useState } from "react";
import { fetchOwnKickChannel, type KickUser } from "../lib/kickApi";
import {
  normalizeKickChannel,
  KICK_CHANNEL_HELP,
} from "../lib/channelValidation";

type Props = {
  user: KickUser;
  onSelect: (channel: string) => void;
  onSignOut: () => void;
};

export function KickChannelPicker({ user, onSelect, onSignOut }: Props) {
  const [ownChannel, setOwnChannel] = useState<string | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [manualChannel, setManualChannel] = useState("");
  const [manualError, setManualError] = useState<string | null>(null);

  useEffect(() => {
    fetchOwnKickChannel()
      .then(setOwnChannel)
      .catch(() =>
        setLoadError(
          "Couldn't load your own channel. You can still enter one manually below.",
        ),
      );
  }, []);

  return (
    <div className="card kick-panel">
      <header className="picker-header">
        <img src={user.profileImageUrl} alt="" className="avatar" />
        <div>
          <p>
            Signed in as <strong>{user.username}</strong>
          </p>
          <button className="link-button" onClick={onSignOut}>
            Sign out
          </button>
        </div>
      </header>

      <h2>Choose a channel</h2>
      <p className="hint">
        Kick doesn't provide a list of channels you moderate, so pick your
        own channel below or type any channel name — Kick still enforces
        who's actually allowed to moderate it.
      </p>

      {loadError && <p className="error">{loadError}</p>}
      {ownChannel && (
        <ul className="channel-list">
          <li>
            <button
              className="channel-button kick-button"
              onClick={() => onSelect(ownChannel)}
            >
              {ownChannel} (you)
            </button>
          </li>
        </ul>
      )}

      <form
        className="manual-channel"
        onSubmit={(e) => {
          e.preventDefault();
          const normalized = normalizeKickChannel(manualChannel);
          if (!normalized) {
            setManualError(`That doesn't look like a Kick channel name. ${KICK_CHANNEL_HELP}`);
            return;
          }
          setManualError(null);
          onSelect(normalized);
        }}
      >
        <label htmlFor="kick-channel-input">Watch a channel by name</label>
        <div className="manual-channel-row">
          <input
            id="kick-channel-input"
            value={manualChannel}
            onChange={(e) => {
              setManualChannel(e.target.value);
              setManualError(null);
            }}
            placeholder="channel name"
            autoComplete="off"
          />
          <button type="submit" disabled={!manualChannel.trim()}>
            Watch
          </button>
        </div>
        {manualError && <p className="error">{manualError}</p>}
      </form>
    </div>
  );
}
