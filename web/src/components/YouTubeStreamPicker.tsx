import { useState } from "react";
import {
  fetchOwnYouTubeLiveChat,
  resolveYouTubeVideo,
  type YouTubeLiveChatRef,
  type YouTubeUser,
} from "../lib/youtubeApi";

type Props = {
  user: YouTubeUser;
  onSelect: (ref: YouTubeLiveChatRef) => void;
  onSignOut: () => void;
};

export function YouTubeStreamPicker({ user, onSelect, onSignOut }: Props) {
  const [ownError, setOwnError] = useState<string | null>(null);
  const [ownLoading, setOwnLoading] = useState(false);
  const [videoInput, setVideoInput] = useState("");
  const [resolveError, setResolveError] = useState<string | null>(null);
  const [resolving, setResolving] = useState(false);

  const pickOwnStream = async () => {
    setOwnLoading(true);
    setOwnError(null);
    try {
      onSelect(await fetchOwnYouTubeLiveChat());
    } catch (err) {
      setOwnError(
        err instanceof Error
          ? err.message
          : "Couldn't find a live stream on your channel right now.",
      );
    } finally {
      setOwnLoading(false);
    }
  };

  const submitVideo = async (e: React.FormEvent) => {
    e.preventDefault();
    const input = videoInput.trim();
    if (!input) return;
    setResolving(true);
    setResolveError(null);
    try {
      onSelect(await resolveYouTubeVideo(input));
    } catch (err) {
      setResolveError(
        err instanceof Error ? err.message : "Couldn't resolve that video.",
      );
    } finally {
      setResolving(false);
    }
  };

  return (
    <div className="card youtube-panel">
      <header className="picker-header">
        <img src={user.profileImageUrl} alt="" className="avatar" />
        <div>
          <p>
            Signed in as <strong>{user.channelTitle}</strong>
          </p>
          <button className="link-button" onClick={onSignOut}>
            Sign out
          </button>
        </div>
      </header>

      <h2>Choose a live stream</h2>
      <p className="hint">
        A YouTube channel doesn't have one persistent chat like Twitch or
        Kick — each broadcast has its own. Watch your own current live
        stream, or paste a live stream's URL or video ID.
      </p>

      <button
        type="button"
        className="channel-button youtube-button"
        disabled={ownLoading}
        onClick={pickOwnStream}
      >
        {ownLoading ? "Looking for your live stream..." : "Watch my current live stream"}
      </button>
      {ownError && <p className="error">{ownError}</p>}

      <form className="manual-channel" onSubmit={submitVideo}>
        <label htmlFor="youtube-video-input">Or watch a live stream by URL/video ID</label>
        <div className="manual-channel-row">
          <input
            id="youtube-video-input"
            value={videoInput}
            onChange={(e) => setVideoInput(e.target.value)}
            placeholder="https://www.youtube.com/watch?v=..."
            autoComplete="off"
          />
          <button type="submit" disabled={!videoInput.trim() || resolving}>
            {resolving ? "Resolving..." : "Watch"}
          </button>
        </div>
        {resolveError && <p className="error">{resolveError}</p>}
      </form>
    </div>
  );
}
