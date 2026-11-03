import { ArrowDownLeft, ArrowUpRight, ChevronRight } from "@gravity-ui/icons";
import { History, Trash2 } from "../components/icons";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { UserPicker } from "../components/EntityPicker";
import { DataTable, type Column } from "../components/DataTable";
import { Alert, Badge, Metric, PageFrame, QueryPanel } from "../components/ui";
import { displayName, formatUnix, parseIDs, toInt } from "../lib/format";
import type { Navigate } from "../routing";
import type { AccountRow, MessageListResponse, MessageRow } from "../types";
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

  // Both people picked: the conversation loads by itself, newest first.
  useEffect(() => {
    if (owner && peer) void load(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [owner, peer]);

  const columns: Column<MessageRow>[] = [
    { key: "id", label: "ID", className: "mono muted-cell", firstDir: "desc", sortValue: (row) => row.BoxID, render: (row) => row.BoxID },
    {
      key: "time",
      label: "Time",
      firstDir: "desc",
      sortValue: (row) => row.Date,
      render: (row) => formatUnix(row.Date)
    },
    {
      key: "direction",
      label: "",
      render: (row) =>
        row.Outgoing ? (
          <span className="msg-dir out" title={"Outgoing"}><ArrowUpRight /></span>
        ) : (
          <span className="msg-dir in" title={"Incoming"}><ArrowDownLeft /></span>
        )
    },
    {
      key: "sender",
      label: "Sender",
      sortValue: (row) => row.FromUserID,
      render: (row) =>
        owner && row.FromUserID === owner.ID ? displayName(owner) : peer && row.FromUserID === peer.ID ? displayName(peer) : <span className="mono">{row.FromUserID}</span>
    },
    { key: "body", label: "Message", className: "truncate", sortValue: (row) => row.Body, render: (row) => row.Body || <span className="muted-cell">{"(no text)"}</span> },
    { key: "status", label: "", render: (row) => (row.Deleted ? <Badge tone="danger">{"Deleted"}</Badge> : null) }
  ];

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
      </QueryPanel>
      {data && (
        <div className="metric-row">
          <Metric label={"Messages on page"} value={String(data.rows.length)} />
          <Metric label={"Outgoing"} value={String(data.rows.filter((row) => row.Outgoing).length)} />
          <Metric label={"Deleted"} value={String(data.rows.filter((row) => row.Deleted).length)} tone={data.rows.some((row) => row.Deleted) ? "danger" : "neutral"} />
        </div>
      )}
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
      <DataTable
        columns={columns}
        rows={data?.rows ?? []}
        rowKey={(row) => `${row.OwnerUserID}-${row.BoxID}`}
        onRowClick={(row) => navigate(`/messages/private/detail?owner_user_id=${row.OwnerUserID}&msg_id=${row.BoxID}`)}
        empty={owner && peer ? "No messages between these two." : "Pick both people above to see their conversation."}
      />
      {data?.rows.length ? (
        <div className="table-footer">
          <span>{`${data.rows.length} messages`}</span>
          <button className="btn icon-text" type="button" onClick={() => void load(true)}>
            {"Older"} <ChevronRight />
          </button>
        </div>
      ) : null}
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
    <PageFrame title={"Messages"}>
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
