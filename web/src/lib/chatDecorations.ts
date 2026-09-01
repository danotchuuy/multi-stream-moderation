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
 * text (imageUrl unset) or an inline emote image, pre-resolved to its
 * CDN URL so the frontend needs no emote catalog of its own. Shared by
 * both Twitch and Kick messages; a message with no emotes carries no
 * fragments at all, and renders from its plain text instead. */
export type ChatFragment = {
  text: string;
  imageUrl?: string;
};
