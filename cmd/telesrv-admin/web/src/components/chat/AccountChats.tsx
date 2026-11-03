import { ArrowRight, Bookmark, Magnifier, Megaphone, Persons, TrashBin } from "@gravity-ui/icons";
import { useEffect, useMemo, useState } from "react";
import { errorMessage } from "../../api";
import { displayUsername, parseIDs, toInt } from "../../lib/format";
import { permissionMessagesManage, useCan } from "../../permissions";
import { ActionButton } from "../ActionButton";
import { privateView, type ChatListItem } from "../../lib/privateView";
import type { Navigate } from "../../routing";
import { Avatar } from "../Avatar";
import { useViewGrant } from "../PrivateGate";
import { ChatView } from "./ChatView";
import { mediaLabel } from "./ChatMedia";

// AccountChats is the account's chat list on the left and the open
// conversation on the right -- the account's own view of its messenger.

function listDate(unix: number): string {
  if (!unix) return "";
  const d = new Date(unix * 1000);
  const now = new Date();
  if (d.toDateString() === now.toDateString()) return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  if (d.getFullYear() === now.getFullYear()) return d.toLocaleDateString(undefined, { day: "numeric", month: "short" });
  return d.toLocaleDateString(undefined, { day: "numeric", month: "short", year: "2-digit" });
}

function chatTitle(chat: ChatListItem): string {
  if (chat.self) return "Saved Messages";
  return chat.title || displayUsername(chat.username) || `${chat.kind === "user" ? "User" : "Chat"} ${chat.peer_id}`;
}

export function AccountChats({ userID, navigate }: { userID: number; navigate: Navigate }) {
  const { grant, onError } = useViewGrant();
  const [chats, setChats] = useState<ChatListItem[] | null>(null);
  const [error, setError] = useState("");
  const [q, setQ] = useState("");
  const [open, setOpen] = useState<ChatListItem | null>(null);
  const [moderating, setModerating] = useState(false);
  const canManage = useCan(permissionMessagesManage);

  useEffect(() => {
    let cancelled = false;
    privateView
      .userChats(userID, grant)
      .then((result) => !cancelled && setChats(result.chats ?? []))
      .catch((err) => !cancelled && !onError(err) && setError(errorMessage(err)));
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userID, grant]);

  const visible = useMemo(() => {
    const needle = q.trim().toLowerCase();
    const list = chats ?? [];
    if (!needle) return list;
    return list.filter((c) => `${chatTitle(c)} ${c.username} ${c.peer_id}`.toLowerCase().includes(needle));
  }, [chats, q]);

  return (
    <div className="messenger">
      <aside className="messenger-list">
        <label className="searchbox">
          <Magnifier />
          <input value={q} onChange={(event) => setQ(event.target.value)} placeholder={"Search chats"} aria-label={"Search chats"} />
        </label>
        {error && <div className="chat-error">{error}</div>}
        {!chats && !error && <div className="chat-empty">{"Loading chats…"}</div>}
        {chats && visible.length === 0 && <div className="chat-empty">{q ? "No chats match." : "No chats."}</div>}
        <ul>
          {visible.map((chat) => {
            const active = open?.kind === chat.kind && open.peer_id === chat.peer_id;
            const preview = chat.last_text || mediaLabel(chat.last_media);
            return (
              <li key={`${chat.kind}:${chat.peer_id}`}>
                <button type="button" className={`messenger-item ${active ? "active" : ""}`} onClick={() => {
                  setOpen(chat);
                  setModerating(false);
                }}>
                  {chat.self ? (
                    <span className="messenger-saved"><Bookmark /></span>
                  ) : (
                    <Avatar id={chat.peer_id} kind={chat.kind === "user" ? "user" : "channel"} firstName={chat.title} title={chat.title} username={chat.username} size={42} />
                  )}
                  <span className="messenger-item-body">
                    <span className="messenger-item-top">
                      <strong>
                        {chat.kind === "channel" && <Megaphone className="messenger-kind" />}
                        {chat.kind === "group" && <Persons className="messenger-kind" />}
                        {chatTitle(chat)}
                      </strong>
                      <time>{listDate(chat.last_date)}</time>
                    </span>
                    <span className="messenger-item-bottom">
                      <span className="messenger-preview">
                        {chat.last_outgoing && <span className="messenger-you">{"You: "}</span>}
                        {preview || " "}
                      </span>
                      {chat.status && chat.status !== "active" && <span className="messenger-status">{chat.status}</span>}
                      {chat.unread ? <span className="messenger-unread">{chat.unread}</span> : null}
                    </span>
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
      </aside>
      <section className="messenger-chat">
        {open ? (
          <>
            <header className="messenger-chat-head">
              {open.self ? (
                <span className="messenger-saved"><Bookmark /></span>
              ) : (
                <Avatar id={open.peer_id} kind={open.kind === "user" ? "user" : "channel"} firstName={open.title} title={open.title} username={open.username} size={36} />
              )}
              <div className="messenger-chat-title">
                <strong>{chatTitle(open)}</strong>
                <span>
                  {open.kind === "user" ? (open.bot ? "bot" : displayUsername(open.username) || `ID ${open.peer_id}`) : `${open.kind} · ${open.role || "member"}`}
                </span>
              </div>
              {canManage && open.kind === "user" && (
                <button className={`btn icon-text ${moderating ? "primary" : ""}`} type="button" onClick={() => setModerating((v) => !v)}>
                  <TrashBin /> {"Delete…"}
                </button>
              )}
              {!open.self && (
                <button
                  className="btn icon-text"
                  type="button"
                  onClick={() => navigate(open.kind === "user" ? `/accounts/${open.peer_id}` : `/channels/${open.peer_id}`)}
                >
                  {"Open profile"} <ArrowRight />
                </button>
              )}
            </header>
            {moderating && open.kind === "user" && <DeletePanel ownerID={userID} peerID={open.peer_id} />}
            <ChatView
              key={`${open.kind}:${open.peer_id}`}
              mode={open.kind === "user" ? "private" : open.kind === "channel" ? "channel" : "group"}
              load={(cursor) => privateView.userChat(userID, open.kind === "user" ? "user" : "channel", open.peer_id, cursor, grant)}
              isOut={(m) => (open.kind === "user" ? Boolean(m.out) : m.from_type === "user" && m.from_id === userID)}
            />
          </>
        ) : (
          <div className="messenger-placeholder">{"Select a chat to read it. Opening a chat is written to the audit log."}</div>
        )}
      </section>
    </div>
  );
}

// DeletePanel holds the two removal commands for one private chat. Both run
// as a dry run first, like every other console action.
function DeletePanel({ ownerID, peerID }: { ownerID: number; peerID: number }) {
  const [ids, setIDs] = useState("");
  const [revoke, setRevoke] = useState(true);
  const [justClear, setJustClear] = useState(false);
  const [maxID, setMaxID] = useState("");
  return (
    <div className="chat-moderation">
      <div className="operation-box">
        <div className="operation-title">{"Delete messages"}</div>
        <input value={ids} onChange={(event) => setIDs(event.target.value)} placeholder={"Message IDs, comma separated"} />
        <label className="checkline"><input type="checkbox" checked={revoke} onChange={(event) => setRevoke(event.target.checked)} /> {"For both sides"}</label>
        <ActionButton path="/api/actions/delete-messages" label={"Dry-run delete"} payload={() => ({
          owner_user_id: ownerID,
          peer_id: peerID,
          ids: parseIDs(ids, "Message IDs are invalid"),
          revoke
        })} />
      </div>
      <div className="operation-box">
        <div className="operation-title">{"Clear history"}</div>
        <input value={maxID} onChange={(event) => setMaxID(event.target.value)} placeholder={"Up to message ID (empty = all)"} />
        <label className="checkline"><input type="checkbox" checked={revoke} onChange={(event) => setRevoke(event.target.checked)} /> {"For both sides"}</label>
        <label className="checkline"><input type="checkbox" checked={justClear} onChange={(event) => setJustClear(event.target.checked)} /> {"Only this side"}</label>
        <ActionButton path="/api/actions/delete-history" label={"Dry-run clear history"} payload={() => ({
          owner_user_id: ownerID,
          peer_id: peerID,
          max_id: toInt(maxID),
          max_batches: 1,
          just_clear: justClear,
          revoke
        })} />
      </div>
    </div>
  );
}
