import type { ChatFragment } from "../lib/chatDecorations";

type Props = {
  text: string;
  fragments?: ChatFragment[];
};

/** Renders a chat message's text, substituting an inline emote image
 * for each fragment that has one — shared by the Twitch and Kick chat
 * panels, the only two platforms whose messages carry fragment data
 * (see chatFragmentDTO in internal/server/handlers_chat.go). Falls back
 * to plain text when there's no fragment breakdown, e.g. a message
 * with no emotes. */
export function ChatMessageText({ text, fragments }: Props) {
  if (!fragments || fragments.length === 0) {
    return <>{text}</>;
  }

  return (
    <>
      {fragments.map((f, i) =>
        f.imageUrl ? (
          <img key={i} className="chat-emote" src={f.imageUrl} alt={f.text} title={f.text} />
        ) : (
          <span key={i}>{f.text}</span>
        ),
      )}
    </>
  );
}
