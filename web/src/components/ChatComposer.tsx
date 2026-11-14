import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { EmotePicker } from "./EmotePicker";
import type { ChatEmote } from "../lib/chatDecorations";

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
  /** Loads the channel's available emotes, shown from a picker button
   * next to the input and used to render a typed/inserted emote name
   * as its image inline in the input itself. Omitted entirely (no
   * picker, no inline rendering) for a platform with no emote catalog
   * of its own. */
  fetchEmotes?: () => Promise<ChatEmote[]>;
  /** How an emote in the input should be encoded in the message text
   * actually sent to the server. Defaults to its plain name, which is
   * all Twitch needs — its own backend matches emote names in message
   * text automatically. Kick has no such server-side matching: it only
   * renders an emote if the message content itself carries its
   * "[emote:id:name]" token syntax (the same syntax incoming Kick
   * messages are parsed from, see kickMessageFragments), so
   * KickChatPanel overrides this to produce that token instead of the
   * bare name. */
  formatEmoteForSend?: (emote: ChatEmote) => string;
};

type Token = { type: "text"; value: string } | { type: "emote"; emote: ChatEmote };

/** Splits plain text into text/emote tokens, converting any whitespace-
 * delimited word that exactly matches a known emote's name — same rule
 * insertEmote already relied on ("only renders when its name stands
 * alone as a whole word"). Re-run on every edit rather than tracked
 * incrementally, so an emote reverts to plain text the moment a
 * keystroke breaks the exact match (e.g. editing a letter inside it). */
function tokenize(text: string, emoteByName: Map<string, ChatEmote>): Token[] {
  if (!text) return [];
  const tokens: Token[] = [];
  for (const part of text.split(/(\s+)/)) {
    if (part === "") continue;
    const emote = emoteByName.get(part);
    tokens.push(emote ? { type: "emote", emote } : { type: "text", value: part });
  }
  return tokens;
}

/** How much of the plain-text character stream one DOM node accounts
 * for — a text node's own length, or (matching tokenize's whole-word
 * rule) an emote image's alt name length. Shared by serialize and the
 * caret offset helpers below so all three agree on the same mapping
 * between DOM position and plain-text position. */
function nodeTextLength(node: Node): number {
  if (node.nodeType === Node.TEXT_NODE) return node.nodeValue?.length ?? 0;
  if (node.nodeType === Node.ELEMENT_NODE && (node as HTMLElement).tagName === "IMG") {
    return (node as HTMLElement).dataset.emoteName?.length ?? 0;
  }
  return 0;
}

/** Reconstructs the plain-text message from the composer's live DOM —
 * the inverse of renderTokens, an inline emote image contributes its
 * emote name rather than (per the DOM) nothing at all. */
function serialize(root: Node): string {
  let out = "";
  root.childNodes.forEach((n) => {
    if (n.nodeType === Node.TEXT_NODE) out += n.nodeValue ?? "";
    else out += nodeTextLength(n) ? (n as HTMLElement).dataset.emoteName : "";
  });
  return out;
}

/** Like serialize, but for the message actually handed to onSend: each
 * emote image is encoded via format (see formatEmoteForSend) instead of
 * always its bare name. Falls back to the bare name for a name that no
 * longer matches a known emote (e.g. the catalog is still loading), same
 * as serialize. */
function serializeForSend(
  root: Node,
  emoteByName: Map<string, ChatEmote>,
  format?: (emote: ChatEmote) => string,
): string {
  if (!format) return serialize(root);
  let out = "";
  root.childNodes.forEach((n) => {
    if (n.nodeType === Node.TEXT_NODE) {
      out += n.nodeValue ?? "";
      return;
    }
    const name = nodeTextLength(n) ? (n as HTMLElement).dataset.emoteName : undefined;
    if (!name) return;
    const emote = emoteByName.get(name);
    out += emote ? format(emote) : name;
  });
  return out;
}

/** Converts a DOM (node, offset) — as reported by the Selection API —
 * into a plain-text character offset, by summing nodeTextLength for
 * everything before it. */
function domOffsetToTextOffset(root: HTMLElement, node: Node, offset: number): number {
  let total = 0;
  let found = false;

  function walk(n: Node): void {
    if (found) return;
    if (n === node) {
      if (n.nodeType === Node.TEXT_NODE) {
        total += offset;
      } else {
        for (let i = 0; i < offset && i < n.childNodes.length; i++) {
          total += nodeTextLength(n.childNodes[i]);
        }
      }
      found = true;
      return;
    }
    if (n.nodeType === Node.TEXT_NODE) {
      total += n.nodeValue?.length ?? 0;
    } else if (n.nodeType === Node.ELEMENT_NODE) {
      if ((n as HTMLElement).tagName === "IMG") {
        total += nodeTextLength(n);
      } else {
        n.childNodes.forEach(walk);
      }
    }
  }

  walk(root);
  return found ? total : serialize(root).length;
}

/** Reads the caret's plain-text offset, or null if the selection isn't
 * currently inside root (e.g. the composer isn't focused). */
function getCaretOffset(root: HTMLElement): number | null {
  const selection = window.getSelection();
  if (!selection || selection.rangeCount === 0) return null;
  const range = selection.getRangeAt(0);
  if (!root.contains(range.startContainer)) return null;
  return domOffsetToTextOffset(root, range.startContainer, range.startOffset);
}

/** Places the caret at a plain-text offset, mapping back onto the
 * rebuilt DOM — landing just after an emote image if the offset falls
 * inside its name, since the caret can't go inside an atomic image. */
function placeCaretAtOffset(root: HTMLElement, targetOffset: number): void {
  const selection = window.getSelection();
  if (!selection) return;
  let remaining = targetOffset;

  function walk(n: Node): boolean {
    if (n.nodeType === Node.TEXT_NODE) {
      const len = n.nodeValue?.length ?? 0;
      if (remaining <= len) {
        const range = document.createRange();
        range.setStart(n, remaining);
        range.collapse(true);
        selection!.removeAllRanges();
        selection!.addRange(range);
        return true;
      }
      remaining -= len;
      return false;
    }
    if (n.nodeType === Node.ELEMENT_NODE && (n as HTMLElement).tagName === "IMG") {
      const len = nodeTextLength(n);
      if (remaining <= len) {
        const range = document.createRange();
        const parent = n.parentNode!;
        range.setStart(parent, Array.prototype.indexOf.call(parent.childNodes, n) + 1);
        range.collapse(true);
        selection!.removeAllRanges();
        selection!.addRange(range);
        return true;
      }
      remaining -= len;
      return false;
    }
    return false;
  }

  const handled = Array.from(root.childNodes).some(walk);
  if (!handled) {
    const range = document.createRange();
    range.selectNodeContents(root);
    range.collapse(false);
    selection.removeAllRanges();
    selection.addRange(range);
  }
}

function renderTokens(root: HTMLElement, tokens: Token[]): void {
  root.innerHTML = "";
  for (const token of tokens) {
    if (token.type === "text") {
      root.appendChild(document.createTextNode(token.value));
    } else {
      const img = document.createElement("img");
      img.src = token.emote.imageUrl;
      img.alt = token.emote.name;
      img.title = token.emote.name;
      img.className = "chat-emote composer-emote";
      img.draggable = false;
      img.contentEditable = "false";
      img.dataset.emoteName = token.emote.name;
      root.appendChild(img);
    }
  }
}

/** A single text input + send button for posting a new message back to a
 * chat, shared by all three platform panels — each just supplies its own
 * onSend bound to the right channel/live chat. When replyTo is set, the
 * message is sent as a threaded reply to that message instead of a
 * standalone one.
 *
 * The input itself is a contenteditable box rather than a plain
 * `<input>`, so a typed or inserted emote name renders as its actual
 * image inline — mirroring Twitch/Kick's own chat boxes — while still
 * behaving like a single-line text field (Enter sends, paste strips
 * newlines). Its DOM is rebuilt from scratch on every edit from the
 * current plain-text content (see tokenize/renderTokens); the plain
 * text is the source of truth, the image nodes are just how an
 * exactly-matching word happens to be displayed. */
export function ChatComposer({ onSend, replyTo, onCancelReply, fetchEmotes, formatEmoteForSend }: Props) {
  const [text, setText] = useState("");
  const [emotes, setEmotes] = useState<ChatEmote[] | null>(null);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);
  const composingRef = useRef(false);

  // Loaded eagerly (rather than lazily like the picker's own copy) so a
  // manually-typed emote name renders inline without the picker ever
  // being opened. Guarded on `emotes` rather than a stable fetchEmotes
  // reference — ChatPanel/KickChatPanel pass a fresh closure each
  // render — so this only actually fetches once per mount.
  useEffect(() => {
    if (!fetchEmotes || emotes !== null) return;
    let cancelled = false;
    fetchEmotes()
      .then((list) => {
        if (!cancelled) setEmotes(list);
      })
      .catch(() => {
        // Inline rendering is a nice-to-have; leave emotes as plain
        // text rather than surfacing an error for it.
      });
    return () => {
      cancelled = true;
    };
  }, [fetchEmotes, emotes]);

  const emoteByName = useMemo(() => {
    const map = new Map<string, ChatEmote>();
    for (const e of emotes ?? []) map.set(e.name, e);
    return map;
  }, [emotes]);

  // Move focus to this channel's composer as soon as a reply target is
  // picked, so the viewer can start typing immediately instead of having
  // to click into the input themselves.
  useEffect(() => {
    if (replyTo) {
      rootRef.current?.focus();
    }
  }, [replyTo?.id]);

  /** Rebuilds the composer's DOM from newText and restores the caret to
   * caretOffset, mapped through the same token boundaries. The single
   * choke point every mutation (typing, paste, emote insert, clear on
   * send) goes through, so the plain-text state and the rendered DOM
   * never drift apart. */
  const applyText = (newText: string, caretOffset: number) => {
    const root = rootRef.current;
    if (root) {
      renderTokens(root, tokenize(newText, emoteByName));
      placeCaretAtOffset(root, caretOffset);
    }
    setText(newText);
  };

  const submit = async () => {
    const root = rootRef.current;
    const message = (root ? serializeForSend(root, emoteByName, formatEmoteForSend) : text).trim();
    if (!message || sending) return;

    setSending(true);
    setError(null);
    try {
      await onSend(message, replyTo?.id);
      applyText("", 0);
      onCancelReply?.();
    } catch (err) {
      setError(err instanceof Error ? err.message : "failed to send message");
    } finally {
      setSending(false);
    }
  };

  const handleInput = () => {
    if (composingRef.current) return;
    const root = rootRef.current;
    if (!root) return;
    const caret = getCaretOffset(root) ?? serialize(root).length;
    applyText(serialize(root), caret);
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === "Enter") {
      e.preventDefault();
      void submit();
    }
  };

  const handlePaste = (e: React.ClipboardEvent<HTMLDivElement>) => {
    e.preventDefault();
    const root = rootRef.current;
    if (!root) return;
    const pasted = e.clipboardData.getData("text/plain").replace(/[\r\n]+/g, " ");
    const caret = getCaretOffset(root) ?? serialize(root).length;
    const current = serialize(root);
    applyText(current.slice(0, caret) + pasted + current.slice(caret), caret + pasted.length);
  };

  const insertEmote = (emote: ChatEmote) => {
    const root = rootRef.current;
    if (!root) return;
    root.focus();
    const current = serialize(root);
    const caret = getCaretOffset(root) ?? current.length;
    const before = current.slice(0, caret);
    const after = current.slice(caret);

    // An emote only actually renders when its name stands alone as a
    // whole word, so it needs surrounding whitespace — insert a leading
    // space unless already at the start of a word, and always a
    // trailing one, both platforms' own chat inputs do the same.
    const leading = before.length > 0 && !/\s$/.test(before) ? " " : "";
    const inserted = leading + emote.name + " ";
    applyText(before + inserted + after, before.length + inserted.length);
  };

  return (
    <form
      className="chat-composer"
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
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
        <div
          ref={rootRef}
          className="chat-composer-input"
          contentEditable={!sending}
          suppressContentEditableWarning
          role="textbox"
          aria-multiline="false"
          data-placeholder={replyTo ? `Reply to ${replyTo.username}...` : "Send a message..."}
          onInput={handleInput}
          onKeyDown={handleKeyDown}
          onPaste={handlePaste}
          onCompositionStart={() => {
            composingRef.current = true;
          }}
          onCompositionEnd={() => {
            composingRef.current = false;
            handleInput();
          }}
        />
        {fetchEmotes && <EmotePicker fetchEmotes={fetchEmotes} onSelect={insertEmote} />}
        <button type="submit" className="button chat-composer-send" disabled={sending || !text.trim()}>
          Send
        </button>
      </div>
    </form>
  );
}
