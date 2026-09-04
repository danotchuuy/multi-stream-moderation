import { useCallback, useEffect, useMemo, useState } from "react";
import "./App.css";
import {
  fetchModeratedChannels,
  fetchSession,
  logout,
  type ModeratedChannel,
  type TwitchUser,
} from "./lib/api";
import {
  fetchKickSession,
  kickLogout,
  type KickUser,
} from "./lib/kickApi";
import {
  fetchYouTubeSession,
  youtubeLogout,
  type YouTubeLiveChatRef,
  type YouTubeUser,
} from "./lib/youtubeApi";
import { PlatformSection } from "./components/PlatformSection";
import { NotificationsPanel } from "./components/NotificationsPanel";
import { ConnectTwitch } from "./components/ConnectTwitch";
import { ChannelPicker } from "./components/ChannelPicker";
import { ChatPanel } from "./components/ChatPanel";
import { ConnectKick } from "./components/ConnectKick";
import { KickChannelPicker } from "./components/KickChannelPicker";
import { KickChatPanel } from "./components/KickChatPanel";
import { ConnectYouTube } from "./components/ConnectYouTube";
import { YouTubeStreamPicker } from "./components/YouTubeStreamPicker";
import { YouTubeChatPanel } from "./components/YouTubeChatPanel";
import { ThemeToggle } from "./components/ThemeToggle";
import { useTheme } from "./lib/useTheme";

const CHANNEL_STORAGE_KEY = "msm.selectedChannel";
const KICK_CHANNEL_STORAGE_KEY = "msm.kick.selectedChannel";
const YOUTUBE_STREAM_STORAGE_KEY = "msm.youtube.selectedStream";

function loadStoredYouTubeStream(): YouTubeLiveChatRef | null {
  const raw = localStorage.getItem(YOUTUBE_STREAM_STORAGE_KEY);
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as YouTubeLiveChatRef;
    if (parsed.liveChatId && parsed.channelId) return parsed;
  } catch {
    // Ignore a corrupted/old-shape value and just start fresh.
  }
  return null;
}

function App() {
  const { theme, toggleTheme } = useTheme();

  // --- Twitch ---
  const [user, setUser] = useState<TwitchUser | null>(null);
  const [loading, setLoading] = useState(true);
  const [authError, setAuthError] = useState<string | null>(null);
  const [channel, setChannel] = useState<string | null>(() =>
    localStorage.getItem(CHANNEL_STORAGE_KEY),
  );
  const [moderatedChannels, setModeratedChannels] = useState<
    ModeratedChannel[] | null
  >(null);
  const [moderatedChannelsError, setModeratedChannelsError] = useState<
    string | null
  >(null);

  // --- Kick ---
  const [kickUser, setKickUser] = useState<KickUser | null>(null);
  const [kickLoading, setKickLoading] = useState(true);
  const [kickAuthError, setKickAuthError] = useState<string | null>(null);
  const [kickChannel, setKickChannel] = useState<string | null>(() =>
    localStorage.getItem(KICK_CHANNEL_STORAGE_KEY),
  );

  // --- YouTube ---
  const [youtubeUser, setYoutubeUser] = useState<YouTubeUser | null>(null);
  const [youtubeLoading, setYoutubeLoading] = useState(true);
  const [youtubeAuthError, setYoutubeAuthError] = useState<string | null>(null);
  const [youtubeStream, setYoutubeStream] = useState<YouTubeLiveChatRef | null>(
    loadStoredYouTubeStream,
  );

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const err = params.get("twitch_error");
    const kickErr = params.get("kick_error");
    const youtubeErr = params.get("youtube_error");
    if (err || kickErr || youtubeErr) {
      if (err) setAuthError(err);
      if (kickErr) setKickAuthError(kickErr);
      if (youtubeErr) setYoutubeAuthError(youtubeErr);
      params.delete("twitch_error");
      params.delete("kick_error");
      params.delete("youtube_error");
      const query = params.toString();
      window.history.replaceState(
        {},
        "",
        window.location.pathname + (query ? `?${query}` : ""),
      );
    }

    fetchSession()
      .then((session) => setUser(session.user ?? null))
      .catch(() => setUser(null))
      .finally(() => setLoading(false));

    fetchKickSession()
      .then((session) => setKickUser(session.user ?? null))
      .catch(() => setKickUser(null))
      .finally(() => setKickLoading(false));

    fetchYouTubeSession()
      .then((session) => setYoutubeUser(session.user ?? null))
      .catch(() => setYoutubeUser(null))
      .finally(() => setYoutubeLoading(false));
  }, []);

  useEffect(() => {
    if (!user) {
      setModeratedChannels(null);
      setModeratedChannelsError(null);
      return;
    }
    fetchModeratedChannels()
      .then(setModeratedChannels)
      .catch(() =>
        setModeratedChannelsError(
          "Couldn't load your moderated channels. You can still enter one manually below.",
        ),
      );
  }, [user]);

  const handleSelectChannel = useCallback((next: string) => {
    setChannel(next);
    localStorage.setItem(CHANNEL_STORAGE_KEY, next);
  }, []);

  const handleChangeChannel = useCallback(() => {
    setChannel(null);
    localStorage.removeItem(CHANNEL_STORAGE_KEY);
  }, []);

  const handleSignOut = useCallback(async () => {
    await logout();
    setUser(null);
    handleChangeChannel();
  }, [handleChangeChannel]);

  const handleSelectKickChannel = useCallback((next: string) => {
    setKickChannel(next);
    localStorage.setItem(KICK_CHANNEL_STORAGE_KEY, next);
  }, []);

  const handleChangeKickChannel = useCallback(() => {
    setKickChannel(null);
    localStorage.removeItem(KICK_CHANNEL_STORAGE_KEY);
  }, []);

  const handleKickSignOut = useCallback(async () => {
    await kickLogout();
    setKickUser(null);
    handleChangeKickChannel();
  }, [handleChangeKickChannel]);

  const handleSelectYouTubeStream = useCallback((ref: YouTubeLiveChatRef) => {
    setYoutubeStream(ref);
    localStorage.setItem(YOUTUBE_STREAM_STORAGE_KEY, JSON.stringify(ref));
  }, []);

  const handleChangeYouTubeStream = useCallback(() => {
    setYoutubeStream(null);
    localStorage.removeItem(YOUTUBE_STREAM_STORAGE_KEY);
  }, []);

  const handleYouTubeSignOut = useCallback(async () => {
    await youtubeLogout();
    setYoutubeUser(null);
    handleChangeYouTubeStream();
  }, [handleChangeYouTubeStream]);

  // A viewer can moderate a channel if it's their own, or if it's in
  // their list of moderated channels from Twitch. Twitch enforces this
  // authoritatively on every moderation call; this only decides whether
  // to show the controls at all.
  const canModerate = useMemo(() => {
    if (!user || !channel) return false;
    const lower = channel.toLowerCase();
    if (user.login.toLowerCase() === lower) return true;
    return (
      moderatedChannels?.some((c) => c.broadcasterLogin.toLowerCase() === lower) ??
      false
    );
  }, [user, channel, moderatedChannels]);

  const viewerIsBroadcaster =
    !!user && !!channel && user.login.toLowerCase() === channel.toLowerCase();

  // Kick has no "channels I moderate" listing, so unlike Twitch we can
  // only be sure moderation controls apply to the viewer's own channel;
  // for any other manually-entered channel we still show them and let
  // Kick's API reject the call if the viewer isn't actually a moderator
  // there.
  const kickViewerIsBroadcaster =
    !!kickUser &&
    !!kickChannel &&
    kickUser.username.toLowerCase() === kickChannel.toLowerCase();

  // Same story as Kick: no "channels I moderate" listing, so this is
  // only reliably knowable for the viewer's own channel.
  const youtubeViewerIsBroadcaster =
    !!youtubeUser &&
    !!youtubeStream &&
    youtubeStream.channelId === youtubeUser.channelId;

  return (
    <main className="app">
      <div className="app-header">
        <h1>Multi-Stream Moderation</h1>
        <ThemeToggle theme={theme} onToggle={toggleTheme} />
      </div>

      <div className="panels">
        <NotificationsPanel />

        <PlatformSection label="Twitch">
          {loading && <p>Loading...</p>}
          {!loading && !user && <ConnectTwitch error={authError} />}
          {!loading && user && !channel && (
            <ChannelPicker
              user={user}
              channels={moderatedChannels}
              loadError={moderatedChannelsError}
              onSelect={handleSelectChannel}
              onSignOut={handleSignOut}
            />
          )}
          {!loading && user && channel && (
            <ChatPanel
              channel={channel}
              canModerate={canModerate}
              viewerIsBroadcaster={viewerIsBroadcaster}
              onChangeChannel={handleChangeChannel}
            />
          )}
        </PlatformSection>

        <PlatformSection label="Kick">
          {kickLoading && <p>Loading...</p>}
          {!kickLoading && !kickUser && <ConnectKick error={kickAuthError} />}
          {!kickLoading && kickUser && !kickChannel && (
            <KickChannelPicker
              user={kickUser}
              onSelect={handleSelectKickChannel}
              onSignOut={handleKickSignOut}
            />
          )}
          {!kickLoading && kickUser && kickChannel && (
            <KickChatPanel
              channel={kickChannel}
              canModerate
              viewerIsBroadcaster={kickViewerIsBroadcaster}
              onChangeChannel={handleChangeKickChannel}
            />
          )}
        </PlatformSection>

        <PlatformSection label="YouTube">
          {youtubeLoading && <p>Loading...</p>}
          {!youtubeLoading && !youtubeUser && (
            <ConnectYouTube error={youtubeAuthError} />
          )}
          {!youtubeLoading && youtubeUser && !youtubeStream && (
            <YouTubeStreamPicker
              user={youtubeUser}
              onSelect={handleSelectYouTubeStream}
              onSignOut={handleYouTubeSignOut}
            />
          )}
          {!youtubeLoading && youtubeUser && youtubeStream && (
            <YouTubeChatPanel
              liveChatId={youtubeStream.liveChatId}
              canModerate
              viewerIsBroadcaster={youtubeViewerIsBroadcaster}
              onChangeStream={handleChangeYouTubeStream}
            />
          )}
        </PlatformSection>
      </div>
    </main>
  );
}

export default App;
