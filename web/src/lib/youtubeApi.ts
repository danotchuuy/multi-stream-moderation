export type YouTubeUser = {
  channelId: string;
  channelTitle: string;
  profileImageUrl: string;
};

export type YouTubeSessionResponse = {
  authenticated: boolean;
  user?: YouTubeUser;
};

export async function fetchYouTubeSession(): Promise<YouTubeSessionResponse> {
  const res = await fetch("/api/auth/youtube/session");
  if (!res.ok) {
    throw new Error(`youtube session check failed: ${res.status}`);
  }
  return res.json();
}

export async function youtubeLogout(): Promise<void> {
  await fetch("/api/auth/youtube/logout", { method: "POST" });
}

/** youtubeLoginUrl points the browser at the Go server's OAuth kickoff
 * endpoint. It must be a real navigation (not fetch) since it ends in a
 * redirect to Google's own login/consent page. */
export function youtubeLoginUrl(): string {
  const returnTo = window.location.origin + window.location.pathname;
  return `/api/auth/youtube/login?return_to=${encodeURIComponent(returnTo)}`;
}

/** A resolved live chat: the opaque ID YouTube's chat APIs key off of,
 * plus the broadcasting channel's ID (used to tell whether the viewer
 * owns that channel). */
export type YouTubeLiveChatRef = {
  liveChatId: string;
  channelId: string;
};

async function readErrorMessage(res: Response, fallback: string): Promise<string> {
  const text = await res.text();
  return text.trim() || fallback;
}

/** fetchOwnYouTubeLiveChat finds the signed-in user's own currently
 * live broadcast. YouTube has no "channels I moderate" listing (like
 * Kick), and unlike Kick a channel doesn't even have one persistent
 * chat — only individual broadcasts do — so this is the only one-click
 * option available. */
export async function fetchOwnYouTubeLiveChat(): Promise<YouTubeLiveChatRef> {
  const res = await fetch("/api/youtube/live-chat/own");
  if (!res.ok) {
    throw new Error(
      await readErrorMessage(res, `failed to load your live chat: ${res.status}`),
    );
  }
  return res.json();
}

export async function resolveYouTubeVideo(video: string): Promise<YouTubeLiveChatRef> {
  const params = new URLSearchParams({ video });
  const res = await fetch(`/api/youtube/live-chat/resolve?${params}`);
  if (!res.ok) {
    throw new Error(await readErrorMessage(res, `failed to resolve video: ${res.status}`));
  }
  return res.json();
}

export type YouTubeChatMessage = {
  id: string;
  text: string;
  displayName: string;
  channelId: string;
  profileImage: string;
  mod: boolean;
  subscriber: boolean;
  broadcaster: boolean;
  timestamp: number;
};

export async function deleteYouTubeChatMessage(messageId: string): Promise<void> {
  const params = new URLSearchParams({ messageId });
  const res = await fetch(`/api/youtube/chat/messages?${params}`, {
    method: "DELETE",
  });
  if (!res.ok) {
    throw new Error(await readErrorMessage(res, `delete failed: ${res.status}`));
  }
}

export async function banYouTubeChatter(
  liveChatId: string,
  channelId: string,
): Promise<void> {
  const res = await fetch("/api/youtube/bans", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ liveChatId, channelId }),
  });
  if (!res.ok) {
    throw new Error(await readErrorMessage(res, `ban failed: ${res.status}`));
  }
}

export async function timeoutYouTubeChatter(
  liveChatId: string,
  channelId: string,
  durationSeconds: number,
): Promise<void> {
  const res = await fetch("/api/youtube/timeouts", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ liveChatId, channelId, durationSeconds }),
  });
  if (!res.ok) {
    throw new Error(await readErrorMessage(res, `timeout failed: ${res.status}`));
  }
}
