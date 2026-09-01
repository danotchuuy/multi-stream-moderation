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
  timestamp: number;
};

async function readErrorMessage(res: Response, fallback: string): Promise<string> {
  const text = await res.text();
  return text.trim() || fallback;
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
