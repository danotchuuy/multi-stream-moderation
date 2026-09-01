/** The parent message a chat message is threaded to as a reply — the
 * same shape for both Twitch (reply-parent-* IRC tags) and Kick (the
 * chat.message.sent webhook's replies_to field), see chatReplyDTO in
 * internal/server/handlers_chat.go. */
export type ChatReply = {
  username: string;
  displayName?: string;
  text: string;
};
