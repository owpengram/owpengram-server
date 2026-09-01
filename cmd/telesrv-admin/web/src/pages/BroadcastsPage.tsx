import { ChevronLeft, ChevronRight, Loader2, RefreshCw, Send } from "lucide-react";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import { CreateBroadcastModal } from "../components/CreateBroadcastModal";
import { Alert, Badge, EmptyRow, Metric, PageFrame } from "../components/ui";
import { formatDate } from "../lib/format";
import type { BroadcastListResponse } from "../types";

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

  return (
    <PageFrame
      title={"Broadcasts"}
      eyebrow={"Announcements sent from the official system account"}
      actions={
        <>
          <button className="btn primary icon-text" type="button" onClick={() => setCreateModalOpen(true)}>
            <Send size={15} /> {"Send broadcast"}
          </button>
          <button className="btn" type="button" onClick={() => void loadFresh()} disabled={busy}>
            <RefreshCw size={15} /> {"Refresh"}
          </button>
        </>
      }
    >
      {error && <Alert>{error}</Alert>}
      <div className="metric-row">
        <Metric label={"Campaigns on page"} value={String(rows.length)} />
        <Metric label={"Still delivering"} value={String(inFlight)} tone={inFlight > 0 ? "warn" : "neutral"} />
      </div>

      <div className="table-wrap">
        <table className="data-table">
          <thead>
            <tr>
              <th>{"ID"}</th>
              <th>{"Message"}</th>
              <th>{"Target"}</th>
              <th>{"Sent"}</th>
              <th>{"Failed"}</th>
              <th>{"Total"}</th>
              <th>{"Created by"}</th>
              <th>{"Created"}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => {
              const delivered = row.SentCount + row.FailedCount;
              const done = row.EnumerationDone && row.TargetCount > 0 && delivered >= row.TargetCount;
              return (
                <tr key={row.ID}>
                  <td className="mono">{row.ID}</td>
                  <td className="truncate">{row.Message}</td>
                  <td>{row.TargetMode === "all" ? <Badge tone="warn">{"All users"}</Badge> : <Badge>{"Selected"}</Badge>}</td>
                  <td>{row.SentCount}</td>
                  <td>{row.FailedCount > 0 ? <Badge tone="danger">{row.FailedCount}</Badge> : row.FailedCount}</td>
                  <td>{row.TargetCount}</td>
                  <td>{row.CreatedBy || "-"}</td>
                  <td>
                    {formatDate(row.CreatedAt)}
                    {!done && <Badge tone="warn">{"Sending"}</Badge>}
                  </td>
                </tr>
              );
            })}
            {rows.length === 0 && <EmptyRow colSpan={8} />}
          </tbody>
        </table>
      </div>

      <div className="toolbar">
        <button className="btn icon-text" type="button" onClick={() => void loadPrev()} disabled={!canGoPrev}>
          <ChevronLeft size={15} /> {"Previous page"}
        </button>
        <button className="btn icon-text" type="button" onClick={() => void loadNext()} disabled={!canGoNext}>
          {busy ? <Loader2 size={15} className="spin" /> : <ChevronRight size={15} />} {"Next page"}
        </button>
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
