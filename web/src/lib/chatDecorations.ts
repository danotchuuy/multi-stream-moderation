/** The parent message a chat message is threaded to as a reply — the
 * same shape for both Twitch (the channel.chat.message EventSub event's
 * reply field) and Kick (the chat.message.sent webhook's replies_to
 * field), see chatReplyDTO in
 * internal/server/handlers_chat.go. */
export type ChatReply = {
  username: string;
  displayName?: string;
  text: string;
};

/** One piece of a chat message's text, as broken down server-side (see
 * chatFragmentDTO in internal/server/handlers_chat.go) — either plain
 * text (imageUrl unset), an inline emote image, or a Twitch chat GIF
 * (gif true), pre-resolved to its CDN/asset URL so the frontend needs
 * no emote catalog of its own. Shared by both Twitch and Kick messages;
 * a message with no emotes/gifs carries no fragments at all, and
 * renders from its plain text instead. */
export type ChatFragment = {
  text: string;
  imageUrl?: string;
  /** Set when imageUrl is a Twitch chat GIF rather than an emote, so it
   * renders larger than an inline emote. Always false/unset for Kick. */
  gif?: boolean;
};

/** One emote a viewer can pick from the chat composer's emote picker
 * (see chatEmoteDTO in internal/server/handlers_chat.go) — shared by
 * both Twitch and Kick's emote-list endpoints. */
export type ChatEmote = {
  id: string;
  name: string;
  imageUrl: string;
  /** Groups emotes for display — Twitch's emote_type ("subscriptions",
   * "follower", "globals", ...) or Kick's "channel"/"global"/"emoji". */
  type?: string;
  /** Set for a Kick emote whose subscribers_only flag suggests the
   * signed-in account may not actually be able to use it — Kick's
   * emote listing isn't scoped to the viewer the way Twitch's is.
   * Always false for Twitch. */
  locked?: boolean;
};
