import type { ChatBadge } from "./badges";
import type { ChatReply } from "./chatDecorations";

export type TwitchUser = {
  login: string;
  displayName: string;
  profileImageUrl: string;
};

export type SessionResponse = {
  authenticated: boolean;
  user?: TwitchUser;
};

export async function fetchSession(): Promise<SessionResponse> {
  const res = await fetch("/api/auth/session");
  if (!res.ok) {
    throw new Error(`session check failed: ${res.status}`);
  }
  return res.json();
}

export async function logout(): Promise<void> {
  await fetch("/api/auth/logout", { method: "POST" });
}

/** twitchLoginUrl points the browser at the Go server's OAuth kickoff
 * endpoint. It must be a real navigation (not fetch) since it ends in a
 * redirect to Twitch's own login/consent page. */
export function twitchLoginUrl(): string {
  const returnTo = window.location.origin + window.location.pathname;
  return `/api/auth/twitch/login?return_to=${encodeURIComponent(returnTo)}`;
}

export type ModeratedChannel = {
  broadcasterId: string;
  broadcasterLogin: string;
  broadcasterName: string;
};

export async function fetchModeratedChannels(): Promise<ModeratedChannel[]> {
  const res = await fetch("/api/twitch/moderated-channels");
  if (!res.ok) {
    throw new Error(`failed to load moderated channels: ${res.status}`);
  }
  const data = (await res.json()) as { channels: ModeratedChannel[] };
  return data.channels;
}

export type ChatMessage = {
  id: string;
  channel: string;
  text: string;
  username: string;
  displayName: string;
  userId: string;
  color: string;
  mod: boolean;
  subscriber: boolean;
  broadcaster: boolean;
  badges: ChatBadge[];
  /** Whether this is the sender's first-ever message in the channel. */
  firstMessage?: boolean;
  /** The parent message this one is replying to, if any. */
  reply?: ChatReply;
  timestamp: number;
};

/** Reads an error response's body as text, falling back to the status
 * code if the body is empty (all the moderation endpoints below return
 * Twitch's own error message as plain text on failure). */
async function readErrorMessage(res: Response, fallback: string): Promise<string> {
  const text = await res.text();
  return text.trim() || fallback;
}

export async function sendChatMessage(channel: string, message: string): Promise<void> {
  const res = await fetch("/api/chat/messages", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ channel, message }),
  });
  if (!res.ok) {
    throw new Error(await readErrorMessage(res, `send failed: ${res.status}`));
  }
}

export async function deleteChatMessage(
  channel: string,
  messageId: string,
): Promise<void> {
  const params = new URLSearchParams({ channel, messageId });
  const res = await fetch(`/api/chat/messages?${params}`, { method: "DELETE" });
  if (!res.ok) {
    throw new Error(await readErrorMessage(res, `delete failed: ${res.status}`));
  }
}

export async function banChatter(
  channel: string,
  userId: string,
  reason?: string,
): Promise<void> {
  const res = await fetch("/api/chat/bans", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ channel, userId, reason }),
  });
  if (!res.ok) {
    throw new Error(await readErrorMessage(res, `ban failed: ${res.status}`));
  }
}

export type ChannelInfo = {
  title: string;
  categoryId: string;
  categoryName: string;
};

export async function fetchChannelInfo(channel: string): Promise<ChannelInfo> {
  const params = new URLSearchParams({ channel });
  const res = await fetch(`/api/twitch/channel-info?${params}`);
  if (!res.ok) {
    throw new Error(
      await readErrorMessage(res, `failed to load channel info: ${res.status}`),
    );
  }
  return res.json();
}

export async function updateChannelInfo(
  channel: string,
  updates: { title?: string; categoryId?: string },
): Promise<void> {
  const res = await fetch("/api/twitch/channel-info", {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ channel, ...updates }),
  });
  if (!res.ok) {
    throw new Error(await readErrorMessage(res, `update failed: ${res.status}`));
  }
}

export type CategoryOption = {
  id: string;
  name: string;
  image: string;
};

export async function searchCategories(query: string): Promise<CategoryOption[]> {
  const params = new URLSearchParams({ query });
  const res = await fetch(`/api/twitch/categories?${params}`);
  if (!res.ok) {
    throw new Error(
      await readErrorMessage(res, `category search failed: ${res.status}`),
    );
  }
  const data = (await res.json()) as { categories: CategoryOption[] };
  return data.categories;
}

export async function timeoutChatter(
  channel: string,
  userId: string,
  durationSeconds: number,
  reason?: string,
): Promise<void> {
  const res = await fetch("/api/chat/timeouts", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ channel, userId, durationSeconds, reason }),
  });
  if (!res.ok) {
    throw new Error(await readErrorMessage(res, `timeout failed: ${res.status}`));
  }
}
