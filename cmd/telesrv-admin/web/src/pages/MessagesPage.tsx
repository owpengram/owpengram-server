import { ChevronRight, Magnifier, Megaphone, Persons } from "@gravity-ui/icons";
import { useEffect, useState } from "react";
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

export function MessagesPage({ navigate, tab, onTab }: { navigate: Navigate; tab: Tab; onTab: (tab: Tab) => void }) {
  const [q, setQ] = useState("");
  const query = useDebounced(q.trim());
  const [accounts, setAccounts] = useState<AccountRow[]>([]);
  const [channels, setChannels] = useState<ChannelRow[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    setBusy(true);
    setError("");
    const params = new URLSearchParams({ limit: "50" });
    if (query) params.set("q", query);
    const request =
      tab === "private"
        ? api.accounts(params).then((r) => !cancelled && setAccounts(r.rows ?? []))
        : api.channels(params).then((r) => !cancelled && setChannels(r.rows ?? []));
    request
      .catch((err) => !cancelled && setError(errorMessage(err)))
      .finally(() => !cancelled && setBusy(false));
    return () => {
      cancelled = true;
    };
  }, [tab, query]);

  return (
    <PageFrame title={"Messages"}>
      <div className="tab-bar" role="tablist" aria-label={"Message sections"}>
        <button className={`tab-btn ${tab === "private" ? "active" : ""}`} type="button" role="tab" aria-selected={tab === "private"} onClick={() => onTab("private")}>
          {"People"}
        </button>
        <button className={`tab-btn ${tab === "groups" ? "active" : ""}`} type="button" role="tab" aria-selected={tab === "groups"} onClick={() => onTab("groups")}>
          {"Groups and channels"}
        </button>
      </div>
      <p className="picker-hint">
        {tab === "private"
          ? "Pick an account to go through its chats. You'll be asked for your password, and the opening is written to the audit log."
          : "Pick a group or channel to read it. You'll be asked for your password, and the opening is written to the audit log."}
      </p>
      <label className="searchbox picker-search">
        <Magnifier />
        <input
          value={q}
          onChange={(event) => setQ(event.target.value)}
          placeholder={tab === "private" ? "Search by name, username, phone or ID" : "Search by title, username or ID"}
          aria-label={"Search"}
          autoFocus
        />
      </label>
      {error && <Alert>{error}</Alert>}
      <ul className="picker-list">
        {tab === "private"
          ? accounts.map((row) => (
              <li key={row.ID}>
                <button type="button" className="picker-item" onClick={() => navigate(`/accounts/${row.ID}?tab=chats`)}>
                  <Avatar id={row.ID} firstName={row.FirstName} lastName={row.LastName} username={row.Username} size={40} />
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
                  <Avatar id={row.ID} kind="channel" title={row.Title} size={40} />
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
      {!busy && !error && (tab === "private" ? accounts : channels).length === 0 && <div className="chat-empty">{"Nothing found."}</div>}
    </PageFrame>
  );
}
