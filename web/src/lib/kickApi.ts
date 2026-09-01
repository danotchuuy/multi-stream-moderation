import type { ChatBadge } from "./badges";
import type { ChatFragment, ChatReply } from "./chatDecorations";

export type KickUser = {
  username: string;
  profileImageUrl: string;
};

export type KickSessionResponse = {
  authenticated: boolean;
  user?: KickUser;
};

export async function fetchKickSession(): Promise<KickSessionResponse> {
  const res = await fetch("/api/auth/kick/session");
  if (!res.ok) {
    throw new Error(`kick session check failed: ${res.status}`);
  }
  return res.json();
}

export async function kickLogout(): Promise<void> {
  await fetch("/api/auth/kick/logout", { method: "POST" });
}

/** kickLoginUrl points the browser at the Go server's OAuth kickoff
 * endpoint. It must be a real navigation (not fetch) since it ends in a
 * redirect to Kick's own login/consent page. */
export function kickLoginUrl(): string {
  const returnTo = window.location.origin + window.location.pathname;
  return `/api/auth/kick/login?return_to=${encodeURIComponent(returnTo)}`;
}

/** fetchOwnKickChannel returns the signed-in user's own channel slug.
 * Kick has no "channels I moderate" listing endpoint the way Twitch
 * does, so this is the only one-click option available. */
export async function fetchOwnKickChannel(): Promise<string> {
  const res = await fetch("/api/kick/channel");
  if (!res.ok) {
    throw new Error(`failed to load kick channel: ${res.status}`);
  }
  const data = (await res.json()) as { slug: string };
  return data.slug;
}

export type KickChatMessage = {
  id: string;
  channel: string;
  text: string;
  username: string;
  userId: number;
  color: string;
  mod: boolean;
  subscriber: boolean;
  broadcaster: boolean;
  badges: ChatBadge[];
  /** The parent message this one is replying to, if any. */
  reply?: ChatReply;
  /** Text broken into plain-text/emote pieces for rendering; absent
   * for a message with no emotes. */
  fragments?: ChatFragment[];
  timestamp: number;
};

async function readErrorMessage(res: Response, fallback: string): Promise<string> {
  const text = await res.text();
  return text.trim() || fallback;
}

/** replyTo, if given, is the message ID being replied to — threads the
 * new message the same way replying in Kick's own chat UI does. */
export async function sendKickChatMessage(
  channel: string,
  message: string,
  replyTo?: string,
): Promise<void> {
  const res = await fetch("/api/kick/chat/messages", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ channel, message, replyTo }),
  });
  if (!res.ok) {
    throw new Error(await readErrorMessage(res, `send failed: ${res.status}`));
  }
}

export async function deleteKickChatMessage(messageId: string): Promise<void> {
  const params = new URLSearchParams({ messageId });
  const res = await fetch(`/api/kick/chat/messages?${params}`, {
    method: "DELETE",
  });
  if (!res.ok) {
    throw new Error(await readErrorMessage(res, `delete failed: ${res.status}`));
  }
}

export type KickChannelInfo = {
  title: string;
  categoryId: number;
  categoryName: string;
};

export async function fetchKickChannelInfo(channel: string): Promise<KickChannelInfo> {
  const params = new URLSearchParams({ channel });
  const res = await fetch(`/api/kick/channel-info?${params}`);
  if (!res.ok) {
    throw new Error(
      await readErrorMessage(res, `failed to load channel info: ${res.status}`),
    );
  }
  return res.json();
}

/** updateKickChannelInfo always applies to the signed-in user's own
 * channel — Kick identifies it from the access token, with no
 * broadcaster ID to pass. */
export async function updateKickChannelInfo(updates: {
  title?: string;
  categoryId?: number;
}): Promise<void> {
  const res = await fetch("/api/kick/channel-info", {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(updates),
  });
  if (!res.ok) {
    throw new Error(await readErrorMessage(res, `update failed: ${res.status}`));
  }
}

export type KickCategoryOption = {
  id: number;
  name: string;
  image: string;
};

export async function searchKickCategories(
  query: string,
): Promise<KickCategoryOption[]> {
  const params = new URLSearchParams({ query });
  const res = await fetch(`/api/kick/categories?${params}`);
  if (!res.ok) {
    throw new Error(
      await readErrorMessage(res, `category search failed: ${res.status}`),
    );
  }
  const data = (await res.json()) as { categories: KickCategoryOption[] };
  return data.categories;
}

export async function banKickChatter(
  channel: string,
  userId: number,
  reason?: string,
): Promise<void> {
  const res = await fetch("/api/kick/bans", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ channel, userId, reason }),
  });
  if (!res.ok) {
    throw new Error(await readErrorMessage(res, `ban failed: ${res.status}`));
  }
}

/** durationMinutes: Kick takes the timeout length in minutes (1-10080),
 * unlike Twitch's seconds. */
export async function timeoutKickChatter(
  channel: string,
  userId: number,
  durationMinutes: number,
  reason?: string,
): Promise<void> {
  const res = await fetch("/api/kick/timeouts", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ channel, userId, durationMinutes, reason }),
  });
  if (!res.ok) {
    throw new Error(await readErrorMessage(res, `timeout failed: ${res.status}`));
  }
}
