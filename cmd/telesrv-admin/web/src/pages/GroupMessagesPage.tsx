import { ChevronRight, Search } from "lucide-react";
import { useState } from "react";
import { api, errorMessage } from "../api";
import { ChannelPicker } from "../components/EntityPicker";
import { Alert, Badge, EmptyRow, Metric, QueryPanel } from "../components/ui";
import { channelKind, formatUnix } from "../lib/format";
import type { Navigate } from "../routing";
import type { ChannelRow, GroupMessageListResponse } from "../types";

export function GroupMessagesTab({ navigate }: { navigate: Navigate }) {
  const [channel, setChannel] = useState<ChannelRow | null>(null);
  const [beforeDate, setBeforeDate] = useState("");
  const [beforeID, setBeforeID] = useState("");
  const [limit, setLimit] = useState("100");
  const [data, setData] = useState<GroupMessageListResponse | null>(null);
  const [error, setError] = useState("");

  async function load(next = false) {
    setError("");
    if (!channel) {
      setError("Search and select a supergroup or channel first");
      return;
    }
    const params = new URLSearchParams({
      channel_id: String(channel.ID),
      limit
    });
    if (next && data?.rows.length) {
      const last = data.rows[data.rows.length - 1];
      params.set("before_date", String(last.Date));
      params.set("before_id", String(last.ID));
      setBeforeDate(String(last.Date));
      setBeforeID(String(last.ID));
    } else {
      if (beforeDate) params.set("before_date", beforeDate);
      if (beforeID) params.set("before_id", beforeID);
    }
    try {
      setData(await api.groupMessages(params));
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  function changeChannel(row: ChannelRow | null) {
    setChannel(row);
    setBeforeDate("");
    setBeforeID("");
    setData(null);
  }

  const rows = data?.rows ?? [];

  return (
    <>
      {error && <Alert>{error}</Alert>}
      <QueryPanel>
        <div className="message-selector-grid single">
          <ChannelPicker label={"Channel / Group"} value={channel} onChange={changeChannel} />
        </div>
        <form className="toolbar message-query" onSubmit={(event) => { event.preventDefault(); void load(false); }}>
          <input value={beforeDate} onChange={(event) => setBeforeDate(event.target.value)} placeholder={"before_date cursor"} />
          <input value={beforeID} onChange={(event) => setBeforeID(event.target.value)} placeholder={"before_msg_id cursor"} />
          <input className="small-input" value={limit} onChange={(event) => setLimit(event.target.value)} placeholder={"limit <= 100"} />
          <button className="btn primary icon-text" type="submit"><Search size={15} /> {"Search messages"}</button>
          {rows.length ? <button className="btn icon-text" type="button" onClick={() => load(true)}><ChevronRight size={15} /> {"Next page"}</button> : null}
        </form>
      </QueryPanel>
      <div className="metric-row">
        <Metric label={"Messages on page"} value={String(rows.length)} />
        <Metric label={"With media"} value={String(rows.filter((row) => row.Media && row.Media !== "{}").length)} />
        <Metric label={"Channel posts"} value={String(rows.filter((row) => row.Post).length)} />
        <Metric label={"Channel / Group"} value={channel ? `${channel.Title || channelKind(channel)} (${channel.ID})` : "-"} />
      </div>
      <div className="table-wrap">
        <table className="data-table">
          <thead>
            <tr>
              <th>{"Message ID"}</th>
              <th>{"Time"}</th>
              <th>{"Sender"}</th>
              <th>From Peer</th>
              <th>PTS</th>
              <th>{"Views"}</th>
              <th>{"Status"}</th>
              <th>{"Body"}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={`${row.ChannelID}-${row.ID}`}>
                <td className="mono">{row.ID}</td>
                <td>{formatUnix(row.Date)}</td>
                <td className="mono">{row.SenderUserID}</td>
                <td className="mono">{row.FromPeerType}:{row.FromPeerID}</td>
                <td>{row.PTS}</td>
                <td>{row.ViewsCount}</td>
                <td>
                  {row.Deleted ? <Badge tone="danger">{"Deleted"}</Badge> : row.Pinned ? <Badge tone="warn">{"Pinned"}</Badge> : <Badge>{"Live"}</Badge>}
                </td>
                <td className="truncate">{row.Body}</td>
                <td>
                  <button
                    className="row-link"
                    onClick={() => navigate(`/messages/groups/detail?channel_id=${row.ChannelID}&msg_id=${row.ID}`)}
                  >
                    {"Details"} <ChevronRight size={14} />
                  </button>
                </td>
              </tr>
            ))}
            {rows.length === 0 && <EmptyRow colSpan={9} />}
          </tbody>
        </table>
      </div>
    </>
  );
}
