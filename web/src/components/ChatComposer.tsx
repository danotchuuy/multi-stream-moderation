import { useState, type FormEvent } from "react";

type Props = {
  onSend: (message: string) => Promise<void>;
};

/** A single text input + send button for posting a new message back to a
 * chat, shared by all three platform panels — each just supplies its own
 * onSend bound to the right channel/live chat. */
export function ChatComposer({ onSend }: Props) {
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const message = text.trim();
    if (!message || sending) return;

    setSending(true);
    setError(null);
    try {
      await onSend(message);
      setText("");
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
      <div className="chat-composer-row">
        <input
          type="text"
          className="chat-composer-input"
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder="Send a message..."
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
