import { ArrowShapeTurnUpRight, Megaphone, Person, Persons } from "@gravity-ui/icons";
import { useEffect, useState } from "react";
import { errorMessage } from "../../api";
import type { ChatMedia, ChatMessage, ChatPeer } from "../../lib/privateView";
import type { Navigate } from "../../routing";
import { Lightbox, MediaView, type MediaURLFor } from "./ChatMedia";
import { serviceText } from "./ChatView";

// ReportEvidence draws what a report is about -- the frozen snapshot the
// reporter submitted -- as it looked in the app. No password is needed: the
// reporter handed it over, and only files the report holds can be loaded.

type EvidenceItem = { kind: string; peer: { type: string; id: number }; message?: ChatMessage };
type EvidenceResponse = { items: EvidenceItem[]; peers: Record<string, ChatPeer> };

const KIND_LABEL: Record<string, string> = {
  message: "Message",
  story: "Story",
  profile_photo: "Profile photo",
  reaction: "Reaction on a message",
  peer: "Whole profile",
  encrypted_chat: "Secret chat",
  sponsored: "Sponsored message"
};

export function ReportEvidence({ reportID, navigate }: { reportID: number; navigate: Navigate }) {
  const [data, setData] = useState<EvidenceResponse | null>(null);
  const [error, setError] = useState("");
  const [open, setOpen] = useState<ChatMedia | null>(null);

  useEffect(() => {
    let cancelled = false;
    setData(null);
    setError("");
    fetch(`/api/moderation/reports/${reportID}/evidence`, { credentials: "same-origin", cache: "no-store" })
      .then(async (r) => {
        const body = await r.json().catch(() => null);
        if (!r.ok) throw new Error(body?.error || r.statusText);
        return body as EvidenceResponse;
      })
      .then((body) => !cancelled && setData(body))
      .catch((err) => !cancelled && setError(errorMessage(err)));
    return () => {
      cancelled = true;
    };
  }, [reportID]);

  const urlFor: MediaURLFor = (key, lottie) =>
    `/api/moderation/reports/${reportID}/media?key=${encodeURIComponent(key)}${lottie ? "&lottie=1" : ""}`;
  const name = (type?: string, id?: number) => (type && id ? data?.peers[`${type}:${id}`]?.name || `${type === "channel" ? "Channel" : "User"} ${id}` : "");

  if (error) return <div className="chat-error">{error}</div>;
  if (!data) return <div className="chat-empty">{"Loading reported content…"}</div>;
  const drawable = data.items.filter((item) => item.message);
  if (drawable.length === 0) {
    return <div className="evidence-note">{"This report is about the whole profile or chat, not a single message."}</div>;
  }

  return (
    <div className="evidence-list">
      {data.items.map((item, index) => {
        const m = item.message;
        if (!m) return null;
        const chatLink = item.peer.type === "channel" ? `/channels/${item.peer.id}` : item.peer.id ? `/accounts/${item.peer.id}` : "";
        return (
          <div key={index} className="evidence-item">
            <div className="evidence-head">
              <span className="evidence-kind">{KIND_LABEL[item.kind] ?? item.kind}</span>
              {item.peer.id > 0 && (
                <button type="button" className="link-btn evidence-chat" onClick={() => chatLink && navigate(chatLink)}>
                  {item.peer.type === "channel" ? <Megaphone /> : item.peer.type === "chat" ? <Persons /> : <Person />}
                  {`in ${name(item.peer.type, item.peer.id)}`}
                </button>
              )}
              {m.id > 0 && <span className="evidence-id">{`#${m.id}`}</span>}
            </div>
            <div className="chat-row in">
              <div className={`chat-bubble ${m.media && !m.text ? "media-only" : ""}`}>
                {m.from_id > 0 && (
                  <button
                    type="button"
                    className="chat-sender link-btn"
                    onClick={() => navigate(m.from_type === "channel" ? `/channels/${m.from_id}` : `/accounts/${m.from_id}`)}
                  >
                    {name(m.from_type, m.from_id)}
                  </button>
                )}
                {(m.fwd_from_id || m.fwd_from_name) && (
                  <div className="chat-forward">
                    <ArrowShapeTurnUpRight /> {"Forwarded from "}
                    <strong>{m.fwd_from_name || name(m.fwd_from_type, m.fwd_from_id)}</strong>
                  </div>
                )}
                {m.reply_to_id ? <div className="chat-reply"><strong>{"Reply"}</strong><span>{`to message #${m.reply_to_id}`}</span></div> : null}
                {m.service && <div className="chat-text muted-cell">{serviceText(m.service)}</div>}
                {m.media && <MediaView media={m.media} grant="" urlFor={urlFor} onOpen={setOpen} />}
                {m.text && <div className="chat-text">{m.text}</div>}
                <div className="chat-meta">
                  {m.edit_date ? <span>{"edited"}</span> : null}
                  {m.date > 0 && <span>{new Date(m.date * 1000).toLocaleString()}</span>}
                </div>
              </div>
            </div>
          </div>
        );
      })}
      {open && <Lightbox media={open} grant="" urlFor={urlFor} onClose={() => setOpen(null)} />}
    </div>
  );
}
