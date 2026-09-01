import { useEffect, useRef, useState, type FormEvent } from "react";

/** The message a reply, once sent, will be threaded under — set by
 * clicking "Reply" on a message in the chat log. */
export type ReplyTarget = {
  id: string;
  username: string;
  text: string;
};

type Props = {
  onSend: (message: string, replyToId?: string) => Promise<void>;
  /** Set while the viewer is composing a reply to a specific message;
   * null for an ordinary new message. */
  replyTo?: ReplyTarget | null;
  /** Clears replyTo, e.g. from the "cancel" link shown while replying. */
  onCancelReply?: () => void;
};

/** A single text input + send button for posting a new message back to a
 * chat, shared by all three platform panels — each just supplies its own
 * onSend bound to the right channel/live chat. When replyTo is set, the
 * message is sent as a threaded reply to that message instead of a
 * standalone one. */
export function ChatComposer({ onSend, replyTo, onCancelReply }: Props) {
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  // Move focus to this channel's composer as soon as a reply target is
  // picked, so the viewer can start typing immediately instead of having
  // to click into the input themselves.
  useEffect(() => {
    if (replyTo) {
      inputRef.current?.focus();
    }
  }, [replyTo?.id]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const message = text.trim();
    if (!message || sending) return;

    setSending(true);
    setError(null);
    try {
      await onSend(message, replyTo?.id);
      setText("");
      onCancelReply?.();
    } catch (err) {
      setError(err instanceof Error ? err.message : "failed to send message");
    } finally {
      setSending(false);
    }
  };

  return (
    <form className="chat-composer" onSubmit={submit}>
      {error && (
        <p className="error mod-error">
          {error}{" "}
          <button type="button" className="link-button" onClick={() => setError(null)}>
            dismiss
          </button>
        </p>
      )}
      {replyTo && (
        <p className="chat-composer-reply">
          Replying to <strong>{replyTo.username}</strong>: {replyTo.text}{" "}
          <button type="button" className="link-button" onClick={onCancelReply}>
            cancel
          </button>
        </p>
      )}
      <div className="chat-composer-row">
        <input
          ref={inputRef}
          type="text"
          className="chat-composer-input"
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={replyTo ? `Reply to ${replyTo.username}...` : "Send a message..."}
          disabled={sending}
        />
        <button
          type="submit"
          className="button chat-composer-send"
          disabled={sending || !text.trim()}
        >
          Send
        </button>
      </div>
    </form>
  );
}
