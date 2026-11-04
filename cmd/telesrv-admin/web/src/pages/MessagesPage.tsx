import { ChevronLeft, ChevronRight, Magnifier, Megaphone, Persons } from "@gravity-ui/icons";
import { useEffect, useRef, useState } from "react";
import { api, errorMessage } from "../api";
import { Avatar } from "../components/Avatar";
import { Alert, PageFrame } from "../components/ui";
import { channelKind, displayName, displayPhone, displayUsername } from "../lib/format";
import { useDebounced } from "../lib/useDebounced";
import type { Navigate } from "../routing";
import type { AccountRow, ChannelRow } from "../types";

// Messages is where reading starts: pick a person to go through their chats,
// or a group/channel to read it. The reading itself happens on the profile,
// behind the password re-check, so every opening lands in the audit log.

type Tab = "private" | "groups";
type PageSize = 24 | 48 | 96;

// Both lists page by keyset: a cursor is the last row's sort key and id.
type Cursor = { id: number; at: number };
const zeroCursor: Cursor = { id: 0, at: 0 };

export function MessagesPage({ navigate, tab, onTab }: { navigate: Navigate; tab: Tab; onTab: (tab: Tab) => void }) {
  const [q, setQ] = useState("");
  const query = useDebounced(q.trim());
  const [limit, setLimit] = useState<PageSize>(48);
  const [accounts, setAccounts] = useState<AccountRow[]>([]);
  const [channels, setChannels] = useState<ChannelRow[]>([]);
  const [next, setNext] = useState<Cursor | null>(null);
  // history holds the cursor of every page before the current one.
  const [history, setHistory] = useState<Cursor[]>([]);
  const [cursor, setCursor] = useState<Cursor>(zeroCursor);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const requestSeq = useRef(0);

  async function fetchPage(at: Cursor): Promise<boolean> {
    const seq = ++requestSeq.current;
    setBusy(true);
    setError("");
    const params = new URLSearchParams({ limit: String(limit) });
    if (query) params.set("q", query);
    if (at.id || at.at) {
      params.set("before_id", String(at.id));
      params.set(tab === "private" ? "before_active_us" : "before_updated_us", String(at.at));
    }
    try {
      if (tab === "private") {
        const r = await api.accounts(params);
        if (seq !== requestSeq.current) return false;
        setAccounts(r.rows ?? []);
        setNext(r.has_more ? { id: r.next_before_id, at: r.next_before_active_us } : null);
      } else {
        const r = await api.channels(params);
        if (seq !== requestSeq.current) return false;
        setChannels(r.rows ?? []);
        setNext(r.has_more ? { id: r.next_before_id, at: r.next_before_updated_us } : null);
      }
      return true;
    } catch (err) {
      if (seq === requestSeq.current) setError(errorMessage(err));
      return false;
    } finally {
      if (seq === requestSeq.current) setBusy(false);
    }
  }

  useEffect(() => {
    setHistory([]);
    setCursor(zeroCursor);
    void fetchPage(zeroCursor);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab, query, limit]);

  async function goNext() {
    if (!next || busy) return;
    const at = next;
    if (await fetchPage(at)) {
      setHistory((prev) => [...prev, cursor]);
      setCursor(at);
    }
  }

  async function goPrev() {
    if (history.length === 0 || busy) return;
    const at = history[history.length - 1];
    if (await fetchPage(at)) {
      setHistory((prev) => prev.slice(0, -1));
      setCursor(at);
    }
  }

  const count = tab === "private" ? accounts.length : channels.length;
  const pageStart = history.length * limit;

  return (
    <PageFrame title={"Messages"}>
      <div className="picker-page">
        <div className="tab-bar" role="tablist" aria-label={"Message sections"}>
          <button className={`tab-btn ${tab === "private" ? "active" : ""}`} type="button" role="tab" aria-selected={tab === "private"} onClick={() => onTab("private")}>
            {"People"}
          </button>
          <button className={`tab-btn ${tab === "groups" ? "active" : ""}`} type="button" role="tab" aria-selected={tab === "groups"} onClick={() => onTab("groups")}>
            {"Groups and channels"}
          </button>
        </div>
        <div className="picker-bar">
          <label className="searchbox messages-picker-search">
            <Magnifier />
            <input
              value={q}
              onChange={(event) => setQ(event.target.value)}
              placeholder={tab === "private" ? "Search by name, username, phone or ID" : "Search by title, username or ID"}
              aria-label={"Search"}
              autoFocus
            />
          </label>
          <p className="picker-hint">
            {tab === "private"
              ? "Pick an account to go through its chats. You'll be asked for your password, and the opening is written to the audit log."
              : "Pick a group or channel to read it. You'll be asked for your password, and the opening is written to the audit log."}
          </p>
        </div>
        {error && <Alert>{error}</Alert>}
        <ul className={`picker-grid ${busy ? "loading" : ""}`}>
          {tab === "private"
            ? accounts.map((row) => (
                <li key={row.ID}>
                  <button type="button" className="picker-item" onClick={() => navigate(`/accounts/${row.ID}?tab=chats`)}>
                    <Avatar id={row.ID} firstName={row.FirstName} lastName={row.LastName} username={row.Username} size={44} />
                    <span className="picker-item-body">
                      <strong>{displayName(row) || `User ${row.ID}`}</strong>
                      <span>{[displayUsername(row.Username), displayPhone(row.Phone), `ID ${row.ID}`].filter(Boolean).join(" · ")}</span>
                    </span>
                    <ChevronRight />
                  </button>
                </li>
              ))
            : channels.map((row) => (
                <li key={row.ID}>
                  <button type="button" className="picker-item" onClick={() => navigate(`/channels/${row.ID}?tab=messages`)}>
                    <Avatar id={row.ID} kind="channel" title={row.Title} size={44} />
                    <span className="picker-item-body">
                      <strong>
                        {row.Megagroup ? <Persons className="messenger-kind" /> : <Megaphone className="messenger-kind" />}
                        {row.Title || `Chat ${row.ID}`}
                      </strong>
                      <span>{[channelKind(row), displayUsername(row.Username), `${row.ParticipantsCount} members`].filter(Boolean).join(" · ")}</span>
                    </span>
                    <ChevronRight />
                  </button>
                </li>
              ))}
        </ul>
        {!busy && !error && count === 0 && <div className="chat-empty">{"Nothing found."}</div>}
        <div className="table-footer picker-footer">
          <span>
            {count > 0 ? `${pageStart + 1}–${pageStart + count}` : ""}
            {!query && (tab === "private" ? " · most recently active first" : " · most recently updated first")}
          </span>
          <div className="pager">
            <span>{"Per page"}</span>
            <select value={String(limit)} onChange={(event) => setLimit(Number(event.target.value) as PageSize)} aria-label={"Per page"}>
              <option value="24">24</option>
              <option value="48">48</option>
              <option value="96">96</option>
            </select>
            <button className="icon-btn" type="button" aria-label={"Previous page"} onClick={() => void goPrev()} disabled={history.length === 0 || busy}>
              <ChevronLeft />
            </button>
            <button className="icon-btn" type="button" aria-label={"Next page"} onClick={() => void goNext()} disabled={!next || busy}>
              <ChevronRight />
            </button>
          </div>
        </div>
      </div>
    </PageFrame>
  );
}
