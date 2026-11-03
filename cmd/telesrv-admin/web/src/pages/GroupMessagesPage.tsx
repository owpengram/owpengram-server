import { ChevronRight, Eye, Pin } from "@gravity-ui/icons";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import { DataTable, type Column } from "../components/DataTable";
import { ChannelPicker } from "../components/EntityPicker";
import { Alert, Badge, Metric, QueryPanel } from "../components/ui";
import { formatUnix } from "../lib/format";
import type { Navigate } from "../routing";
import type { ChannelRow, GroupMessageListResponse, GroupMessageRow } from "../types";

export function GroupMessagesTab({ navigate }: { navigate: Navigate }) {
  const [channel, setChannel] = useState<ChannelRow | null>(null);
  const [data, setData] = useState<GroupMessageListResponse | null>(null);
  const [error, setError] = useState("");

  // Newest first; "Older" continues from the last row on the page.
  async function load(next = false) {
    setError("");
    if (!channel) return;
    const params = new URLSearchParams({ channel_id: String(channel.ID), limit: "100" });
    if (next && data?.rows.length) {
      const last = data.rows[data.rows.length - 1];
      params.set("before_date", String(last.Date));
      params.set("before_id", String(last.ID));
    }
    try {
      setData(await api.groupMessages(params));
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  // Picking a channel opens its history straight away.
  useEffect(() => {
    setData(null);
    if (channel) void load(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channel]);

  const rows = data?.rows ?? [];

  const columns: Column<GroupMessageRow>[] = [
    { key: "id", label: "ID", className: "mono muted-cell", firstDir: "desc", sortValue: (row) => row.ID, render: (row) => row.ID },
    { key: "time", label: "Time", firstDir: "desc", sortValue: (row) => row.Date, render: (row) => formatUnix(row.Date) },
    {
      key: "from",
      label: "From",
      className: "mono",
      sortValue: (row) => row.SenderUserID,
      render: (row) => (row.Post ? <span className="muted-cell">{"channel post"}</span> : row.SenderUserID)
    },
    { key: "body", label: "Message", className: "truncate", sortValue: (row) => row.Body, render: (row) => row.Body || <span className="muted-cell">{"(no text)"}</span> },
    {
      key: "views",
      label: "Views",
      align: "right",
      firstDir: "desc",
      sortValue: (row) => row.ViewsCount,
      render: (row) => (row.ViewsCount ? <span className="cell-identity"><Eye width={12} height={12} />{row.ViewsCount.toLocaleString()}</span> : "")
    },
    {
      key: "status",
      label: "",
      render: (row) => (
        <span className="cell-badges">
          {row.Pinned && <Badge tone="warn"><Pin width={11} height={11} /> {"Pinned"}</Badge>}
          {row.Deleted && <Badge tone="danger">{"Deleted"}</Badge>}
        </span>
      )
    }
  ];

  return (
    <>
      {error && <Alert>{error}</Alert>}
      <QueryPanel>
        <div className="message-selector-grid single">
          <ChannelPicker label={"Channel or group"} value={channel} onChange={setChannel} />
        </div>
      </QueryPanel>
      {data && (
        <div className="metric-row">
          <Metric label={"Messages on page"} value={String(rows.length)} />
          <Metric label={"With media"} value={String(rows.filter((row) => row.Media && row.Media !== "{}").length)} />
          <Metric label={"Channel posts"} value={String(rows.filter((row) => row.Post).length)} />
        </div>
      )}
      <DataTable
        columns={columns}
        rows={rows}
        rowKey={(row) => `${row.ChannelID}-${row.ID}`}
        onRowClick={(row) => navigate(`/messages/groups/detail?channel_id=${row.ChannelID}&msg_id=${row.ID}`)}
        empty={channel ? "No messages here." : "Pick a channel or group above to see its history."}
      />
      {rows.length ? (
        <div className="table-footer">
          <span>{`${rows.length} messages`}</span>
          <button className="btn icon-text" type="button" onClick={() => void load(true)}>
            {"Older"} <ChevronRight />
          </button>
        </div>
      ) : null}
    </>
  );
}
