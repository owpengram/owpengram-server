import { ChevronRight, History, Search, Trash2 } from "lucide-react";
import { useState } from "react";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { UserPicker } from "../components/EntityPicker";
import { Alert, Badge, EmptyRow, Metric, PageFrame, QueryPanel } from "../components/ui";
import { displayName, formatUnix, parseIDs, toInt } from "../lib/format";
import type { Navigate } from "../routing";
import type { AccountRow, MessageListResponse } from "../types";
import { GroupMessagesTab } from "./GroupMessagesPage";

export function PrivateMessagesTab({ navigate }: { navigate: Navigate }) {
  const [owner, setOwner] = useState<AccountRow | null>(null);
  const [peer, setPeer] = useState<AccountRow | null>(null);
  const [beforeDate, setBeforeDate] = useState("");
  const [beforeID, setBeforeID] = useState("");
  const [limit, setLimit] = useState("100");
  const [ids, setIDs] = useState("");
  const [revoke, setRevoke] = useState(true);
  const [justClear, setJustClear] = useState(false);
  const [maxID, setMaxID] = useState("");
  const [maxBatches, setMaxBatches] = useState("1");
  const [data, setData] = useState<MessageListResponse | null>(null);
  const [error, setError] = useState("");

  async function load(next = false) {
    setError("");
    if (!owner || !peer) {
      setError("Search and select the owner user and peer user first");
      return;
    }
    const params = new URLSearchParams({
      owner_user_id: String(owner.ID),
      peer_id: String(peer.ID),
      limit
    });
    if (next && data?.rows.length) {
      const last = data.rows[data.rows.length - 1];
      params.set("before_date", String(last.Date));
      params.set("before_id", String(last.BoxID));
      setBeforeDate(String(last.Date));
      setBeforeID(String(last.BoxID));
    } else {
      if (beforeDate) params.set("before_date", beforeDate);
      if (beforeID) params.set("before_id", beforeID);
    }
    try {
      setData(await api.messages(params));
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  function changeOwner(row: AccountRow | null) {
    setOwner(row);
    setBeforeDate("");
    setBeforeID("");
    setData(null);
  }

  function changePeer(row: AccountRow | null) {
    setPeer(row);
    setBeforeDate("");
    setBeforeID("");
    setData(null);
  }

  return (
    <>
      {error && <Alert>{error}</Alert>}
      <QueryPanel>
        <div className="message-selector-grid">
          <UserPicker label={"Owner user"} value={owner} onChange={changeOwner} />
          <UserPicker label={"Peer user"} value={peer} onChange={changePeer} />
        </div>
        <form className="toolbar message-query" onSubmit={(event) => { event.preventDefault(); void load(false); }}>
          <input value={beforeDate} onChange={(event) => setBeforeDate(event.target.value)} placeholder={"before_date cursor"} />
          <input value={beforeID} onChange={(event) => setBeforeID(event.target.value)} placeholder={"before_msg_id cursor"} />
          <input className="small-input" value={limit} onChange={(event) => setLimit(event.target.value)} placeholder={"limit <= 100"} />
          <button className="btn primary icon-text" type="submit"><Search size={15} /> {"Search messages"}</button>
          {data?.rows.length ? <button className="btn icon-text" type="button" onClick={() => load(true)}><ChevronRight size={15} /> {"Next page"}</button> : null}
        </form>
      </QueryPanel>
      <div className="metric-row">
        <Metric label={"Messages on page"} value={String(data?.rows.length ?? 0)} />
        <Metric label={"Deleted"} value={String((data?.rows ?? []).filter((row) => row.Deleted).length)} tone="danger" />
        <Metric label={"Outgoing"} value={String((data?.rows ?? []).filter((row) => row.Outgoing).length)} />
        <Metric label={"Owner / Peer"} value={owner && peer ? `${displayName(owner)} / ${displayName(peer)}` : "-"} />
      </div>
      <div className="operation-row">
        <div className="operation-box">
          <div className="operation-title"><Trash2 size={15} /> {"Delete selected messages"}</div>
          <input value={ids} onChange={(event) => setIDs(event.target.value)} placeholder={"Message IDs, comma separated"} />
          <label className="checkline"><input type="checkbox" checked={revoke} onChange={(event) => setRevoke(event.target.checked)} /> {"Revoke for both sides"}</label>
          <ActionButton path="/api/actions/delete-messages" label={"Dry-run delete"} payload={() => ({
            owner_user_id: owner?.ID ?? 0,
            peer_id: peer?.ID ?? 0,
            ids: parseIDs(ids, "Message IDs are invalid"),
            revoke
          })} />
        </div>
        <div className="operation-box">
          <div className="operation-title"><History size={15} /> {"Clear private history"}</div>
          <input value={maxID} onChange={(event) => setMaxID(event.target.value)} placeholder={"max_id cutoff"} />
          <input value={maxBatches} onChange={(event) => setMaxBatches(event.target.value)} placeholder={"max_batches"} />
          <label className="checkline"><input type="checkbox" checked={revoke} onChange={(event) => setRevoke(event.target.checked)} /> {"Revoke for both sides"}</label>
          <label className="checkline"><input type="checkbox" checked={justClear} onChange={(event) => setJustClear(event.target.checked)} /> {"Clear only this side"}</label>
          <ActionButton path="/api/actions/delete-history" label={"Dry-run clear history"} payload={() => ({
            owner_user_id: owner?.ID ?? 0,
            peer_id: peer?.ID ?? 0,
            max_id: toInt(maxID),
            max_batches: toInt(maxBatches),
            just_clear: justClear,
            revoke
          })} />
        </div>
      </div>
      <div className="table-wrap">
        <table className="data-table">
          <thead>
            <tr>
              <th>{"Message ID"}</th>
              <th>{"Time"}</th>
              <th>{"Sender"}</th>
              <th>{"Direction"}</th>
              <th>PTS</th>
              <th>{"Status"}</th>
              <th>{"Body"}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {data?.rows.map((row) => (
              <tr key={`${row.OwnerUserID}-${row.BoxID}`}>
                <td className="mono">{row.BoxID}</td>
                <td>{formatUnix(row.Date)}</td>
                <td className="mono">{row.FromUserID}</td>
                <td>{row.Outgoing ? "Outgoing" : "Incoming"}</td>
                <td>{row.PTS}</td>
                <td>{row.Deleted ? <Badge tone="danger">{"Deleted"}</Badge> : <Badge>{"Live"}</Badge>}</td>
                <td className="truncate">{row.Body}</td>
                <td>
                  <button
                    className="row-link"
                    onClick={() => navigate(`/messages/private/detail?owner_user_id=${row.OwnerUserID}&msg_id=${row.BoxID}`)}
                  >
                    {"Details"} <ChevronRight size={14} />
                  </button>
                </td>
              </tr>
            ))}
            {(!data || data.rows.length === 0) && <EmptyRow colSpan={8} />}
          </tbody>
        </table>
      </div>
    </>
  );
}


// The two message stores are one screen with two tabs rather than two sidebar
// entries: they are the same job ("look at what was said") over different peer
// kinds, and a nested menu made that look like two unrelated sections.
export function MessagesPage({ navigate, tab, onTab }: {
  navigate: Navigate;
  tab: "private" | "groups";
  onTab: (tab: "private" | "groups") => void;
}) {
  return (
    <PageFrame title={"Messages"} eyebrow={"Message boxes and channel history"}>
      <div className="tab-bar" role="tablist" aria-label={"Message sections"}>
        <button
          className={`tab-btn ${tab === "private" ? "active" : ""}`}
          type="button"
          role="tab"
          aria-selected={tab === "private"}
          onClick={() => onTab("private")}
        >
          {"Private"}
        </button>
        <button
          className={`tab-btn ${tab === "groups" ? "active" : ""}`}
          type="button"
          role="tab"
          aria-selected={tab === "groups"}
          onClick={() => onTab("groups")}
        >
          {"Groups and channels"}
        </button>
      </div>
      {tab === "private" ? <PrivateMessagesTab navigate={navigate} /> : <GroupMessagesTab navigate={navigate} />}
    </PageFrame>
  );
}
