import { ArrowsRotateRight, ChevronLeft, ChevronRight, PaperPlane } from "@gravity-ui/icons";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import { CreateBroadcastModal } from "../components/CreateBroadcastModal";
import { DataTable, type Column } from "../components/DataTable";
import { Alert, Badge, Metric, PageFrame } from "../components/ui";
import { formatDate, formatRelative } from "../lib/format";
import type { BroadcastListResponse, BroadcastRow } from "../types";

type Cursor = { beforeID: number };
const zeroCursor: Cursor = { beforeID: 0 };

export function BroadcastsPage() {
  const [data, setData] = useState<BroadcastListResponse | null>(null);
  const [history, setHistory] = useState<Cursor[]>([]);
  const [cursor, setCursor] = useState<Cursor>(zeroCursor);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [createModalOpen, setCreateModalOpen] = useState(false);

  async function fetchPage(at: Cursor) {
    setBusy(true);
    setError("");
    const params = new URLSearchParams({ limit: "50" });
    if (at.beforeID) {
      params.set("before_id", String(at.beforeID));
    }
    try {
      const result = await api.broadcasts(params);
      setData(result);
      return result;
    } catch (err) {
      setError(errorMessage(err));
      return null;
    } finally {
      setBusy(false);
    }
  }

  async function loadFresh() {
    setHistory([]);
    setCursor(zeroCursor);
    await fetchPage(zeroCursor);
  }

  async function loadNext() {
    if (!data?.has_more) return;
    const at = { beforeID: data.next_before_id };
    const result = await fetchPage(at);
    if (result) {
      setHistory((prev) => [...prev, cursor]);
      setCursor(at);
    }
  }

  async function loadPrev() {
    if (history.length === 0) return;
    const at = history[history.length - 1];
    const result = await fetchPage(at);
    if (result) {
      setHistory((prev) => prev.slice(0, -1));
      setCursor(at);
    }
  }

  useEffect(() => {
    void loadFresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const rows = data?.rows ?? [];
  const inFlight = rows.filter((row) => !row.EnumerationDone || row.SentCount + row.FailedCount < row.TargetCount).length;
  const canGoPrev = history.length > 0 && !busy;
  const canGoNext = Boolean(data?.has_more) && !busy;

  const columns: Column<BroadcastRow>[] = [
    { key: "message", label: "Message", className: "truncate", sortValue: (row) => row.Message, render: (row) => row.Message },
    {
      key: "target",
      label: "Audience",
      sortValue: (row) => row.TargetMode,
      render: (row) => (row.TargetMode === "all" ? "All users" : "Selected")
    },
    {
      key: "progress",
      label: "Delivered",
      firstDir: "desc",
      sortValue: (row) => (row.TargetCount ? row.SentCount / row.TargetCount : 0),
      render: (row) => {
        const delivered = row.SentCount + row.FailedCount;
        const done = row.EnumerationDone && row.TargetCount > 0 && delivered >= row.TargetCount;
        const pct = row.TargetCount > 0 ? Math.round((row.SentCount / row.TargetCount) * 100) : 0;
        return (
          <span className="cell-identity">
            <span className="mono">{`${row.SentCount.toLocaleString()} / ${row.TargetCount.toLocaleString()}`}</span>
            <span className="cell-bar" aria-hidden="true"><i style={{ width: `${pct}%` }} /></span>
            {!done && <Badge tone="warn">{"Sending"}</Badge>}
          </span>
        );
      }
    },
    {
      key: "failed",
      label: "Failed",
      align: "right",
      firstDir: "desc",
      sortValue: (row) => row.FailedCount,
      render: (row) => (row.FailedCount > 0 ? <Badge tone="danger">{row.FailedCount}</Badge> : "")
    },
    { key: "by", label: "Sent by", sortValue: (row) => row.CreatedBy, render: (row) => row.CreatedBy || <span className="muted-cell">—</span> },
    {
      key: "created",
      label: "Created",
      firstDir: "desc",
      sortValue: (row) => new Date(row.CreatedAt).getTime() || 0,
      render: (row) => <span title={formatDate(row.CreatedAt)}>{formatRelative(row.CreatedAt)}</span>
    }
  ];

  return (
    <PageFrame
      title={"Broadcasts"}
      actions={
        <>
          <button className="btn primary icon-text" type="button" onClick={() => setCreateModalOpen(true)}>
            <PaperPlane /> {"Send broadcast"}
          </button>
          <button className="icon-btn" type="button" aria-label={"Refresh"} title={"Refresh"} onClick={() => void loadFresh()} disabled={busy}>
            <ArrowsRotateRight className={busy ? "spin" : undefined} />
          </button>
        </>
      }
    >
      {error && <Alert>{error}</Alert>}
      <div className="metric-row">
        <Metric label={"Campaigns on page"} value={String(rows.length)} loading={!data} />
        <Metric label={"Still delivering"} value={String(inFlight)} tone={inFlight > 0 ? "warn" : "neutral"} />
      </div>

      <DataTable
        columns={columns}
        rows={rows}
        rowKey={(row) => row.ID}
        loading={busy && !data}
        empty={"No broadcasts sent yet."}
      />
      <div className="table-footer">
        <span>{"Sent from the official system account, newest first"}</span>
        <div className="pager">
          <button className="icon-btn" type="button" aria-label={"Previous page"} onClick={() => void loadPrev()} disabled={!canGoPrev}>
            <ChevronLeft />
          </button>
          <button className="icon-btn" type="button" aria-label={"Next page"} onClick={() => void loadNext()} disabled={!canGoNext}>
            <ChevronRight />
          </button>
        </div>
      </div>

      {createModalOpen && (
        <CreateBroadcastModal
          onClose={() => setCreateModalOpen(false)}
          onCreated={() => void loadFresh()}
        />
      )}
    </PageFrame>
  );
}
