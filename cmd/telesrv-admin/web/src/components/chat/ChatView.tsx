import { ArrowShapeTurnUpRight, Comments, Eye, Pin, TrashBin } from "@gravity-ui/icons";
import { Fragment, useEffect, useLayoutEffect, useRef, useState } from "react";
import { errorMessage } from "../../api";
import { cursorAfter, type ChatMedia, type ChatMessage, type ChatPage, type ChatReply, type Cursor } from "../../lib/privateView";
import { useViewGrant } from "../PrivateGate";
import { Lightbox, MediaView, mediaLabel } from "./ChatMedia";

// ChatView draws one conversation the way a messenger does: oldest at the
// top, newest at the bottom, scrolled to the end, older history loading as
// you scroll up. It knows nothing about where pages come from.

export type ChatMode = "private" | "group" | "channel";

const dayFormat = new Intl.DateTimeFormat(undefined, { day: "numeric", month: "long", year: "numeric" });
const timeFormat = new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit" });

function dayKey(unix: number): string {
  const d = new Date(unix * 1000);
  return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
}

export function serviceText(kind: string): string {
  const text = kind.replace(/_/g, " ").trim();
  return text ? text.charAt(0).toUpperCase() + text.slice(1) : "Service message";
}

// One stable colour per sender, like a client's group chat.
const NAME_COLORS = ["#ff8a80", "#ffb74d", "#c58af9", "#64d4a4", "#6cb8ff", "#4dd0e1", "#f48fb1"];
function nameColor(id: number): string {
  return NAME_COLORS[Math.abs(id) % NAME_COLORS.length];
}

export function ChatView({
  load,
  mode,
  isOut,
  onOpenComments,
  emptyText = "No messages."
}: {
  load: (cursor: Cursor) => Promise<ChatPage>;
  mode: ChatMode;
  isOut: (m: ChatMessage) => boolean;
  onOpenComments?: (m: ChatMessage) => void;
  emptyText?: string;
}) {
  const { grant, onError } = useViewGrant();
  // Kept newest-first, as the server pages them; drawn reversed.
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [peers, setPeers] = useState<ChatPage["peers"]>({});
  const [replies, setReplies] = useState<ChatPage["replies"]>({});
  const [cursor, setCursor] = useState<Cursor>(null);
  const [hasMore, setHasMore] = useState(false);
  const [busy, setBusy] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState("");
  const [lightbox, setLightbox] = useState<ChatMedia | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  // Where to put the scroll after a render: the bottom on first load, the
  // same message on screen after older history is prepended.
  const anchor = useRef<{ kind: "bottom" } | { kind: "keep"; height: number; top: number } | null>(null);

  async function fetchMore(at: Cursor, first: boolean) {
    setBusy(true);
    setError("");
    try {
      const page = await load(at);
      const el = scroller.current;
      anchor.current = first || !el ? { kind: "bottom" } : { kind: "keep", height: el.scrollHeight, top: el.scrollTop };
      setMessages((prev) => (first ? page.messages : [...prev, ...page.messages]));
      setPeers((prev) => ({ ...prev, ...page.peers }));
      setReplies((prev) => ({ ...prev, ...page.replies }));
      setCursor(cursorAfter(page));
      setHasMore(page.has_more);
      setLoaded(true);
    } catch (err) {
      if (!onError(err)) setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void fetchMore(null, true);
    // The parent remounts this per conversation (key), so load is fixed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useLayoutEffect(() => {
    const el = scroller.current;
    const a = anchor.current;
    if (!el || !a) return;
    if (a.kind === "bottom") el.scrollTop = el.scrollHeight;
    else el.scrollTop = el.scrollHeight - a.height + a.top;
    anchor.current = null;
  }, [messages]);

  function onScroll() {
    const el = scroller.current;
    if (el && el.scrollTop < 120 && hasMore && !busy) void fetchMore(cursor, false);
  }

  const peerName = (type: string | undefined, id: number | undefined) => {
    if (!type || !id) return "";
    return peers[`${type}:${id}`]?.name || `${type === "channel" ? "Channel" : "User"} ${id}`;
  };

  const ordered = [...messages].reverse();

  return (
    <div className="chat-view">
      <div className="chat-scroll" ref={scroller} onScroll={onScroll}>
        {hasMore && (
          <div className="chat-more">
            <button className="btn" type="button" disabled={busy} onClick={() => void fetchMore(cursor, false)}>
              {busy ? "Loading…" : "Load older messages"}
            </button>
          </div>
        )}
        {error && <div className="chat-error">{error}</div>}
        {loaded && ordered.length === 0 && !error && <div className="chat-empty">{emptyText}</div>}
        {!loaded && !error && <div className="chat-empty">{"Loading…"}</div>}
        {ordered.map((m, index) => {
          const prev = ordered[index - 1];
          const newDay = !prev || dayKey(prev.date) !== dayKey(m.date);
          const out = isOut(m);
          const sameSender = prev && !newDay && !prev.service && prev.from_type === m.from_type && prev.from_id === m.from_id;
          const showName = mode === "group" && !out && !m.service && !sameSender;
          const replyMsg = m.reply_to_id ? messages.find((x) => x.id === m.reply_to_id) : undefined;
          const reply: ChatReply | undefined = m.reply_to_id
            ? replies[String(m.reply_to_id)] ??
              (replyMsg && { from_type: replyMsg.from_type, from_id: replyMsg.from_id, text: replyMsg.text, media: replyMsg.media?.kind })
            : undefined;
          return (
            <Fragment key={m.id}>
              {newDay && <div className="chat-day"><span>{dayFormat.format(new Date(m.date * 1000))}</span></div>}
              {m.service ? (
                <div className="chat-service">
                  <span>
                    {mode !== "channel" && m.from_id ? `${peerName(m.from_type, m.from_id)}: ` : ""}
                    {serviceText(m.service)}
                  </span>
                </div>
              ) : (
                <div className={`chat-row ${out ? "out" : "in"} ${mode === "channel" ? "post" : ""}`}>
                  <div className={`chat-bubble ${m.deleted ? "deleted" : ""} ${m.media && !m.text ? "media-only" : ""}`}>
                    {showName && (
                      <div className="chat-sender" style={{ color: nameColor(m.from_id) }}>
                        {peerName(m.from_type, m.from_id)}
                      </div>
                    )}
                    {(m.fwd_from_id || m.fwd_from_name) && (
                      <div className="chat-forward">
                        <ArrowShapeTurnUpRight /> {"Forwarded from "}
                        <strong>{m.fwd_from_name || peerName(m.fwd_from_type, m.fwd_from_id)}</strong>
                      </div>
                    )}
                    {m.reply_to_id ? (
                      <div className="chat-reply">
                        <strong style={reply ? { color: nameColor(reply.from_id) } : undefined}>
                          {reply ? peerName(reply.from_type, reply.from_id) : "Reply"}
                        </strong>
                        <span>{reply ? reply.text || mediaLabel(reply.media) : `Message #${m.reply_to_id}`}</span>
                      </div>
                    ) : null}
                    {m.media && <MediaView media={m.media} grant={grant} onOpen={setLightbox} />}
                    {m.text && <div className="chat-text">{m.text}</div>}
                    <div className="chat-meta">
                      {m.deleted && (
                        <span className="chat-flag danger" title={"Deleted by the user; kept on the server"}>
                          <TrashBin /> {"deleted"}
                        </span>
                      )}
                      {m.pinned && <Pin className="chat-pin" />}
                      {mode === "channel" && m.post_author && <span>{m.post_author}</span>}
                      {m.views ? (
                        <span className="chat-views">
                          <Eye /> {m.views.toLocaleString()}
                        </span>
                      ) : null}
                      {m.edit_date ? <span>{"edited"}</span> : null}
                      <span title={new Date(m.date * 1000).toLocaleString()}>{timeFormat.format(new Date(m.date * 1000))}</span>
                    </div>
                  </div>
                  {mode === "channel" && m.has_thread && onOpenComments && (
                    <button type="button" className="chat-comments" onClick={() => onOpenComments(m)}>
                      <Comments /> {m.comments ? `${m.comments} comment${m.comments === 1 ? "" : "s"}` : "Comments"}
                    </button>
                  )}
                </div>
              )}
            </Fragment>
          );
        })}
      </div>
      {lightbox && <Lightbox media={lightbox} grant={grant} onClose={() => setLightbox(null)} />}
    </div>
  );
}
