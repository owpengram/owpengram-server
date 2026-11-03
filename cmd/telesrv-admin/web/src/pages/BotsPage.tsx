import { ArrowsRotateRight, ChevronLeft, ChevronRight, Magnifier, Plus } from "@gravity-ui/icons";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import { Avatar } from "../components/Avatar";
import { CreateBotModal } from "../components/CreateBotModal";
import { DataTable, FilterChip, type Column } from "../components/DataTable";
import { Alert, Badge, PageFrame } from "../components/ui";
import { ScamFakeBadges } from "../components/flags";
import { displayUsername, formatDate, formatRelative } from "../lib/format";
import { useDebounced } from "../lib/useDebounced";
import type { Navigate } from "../routing";
import type { BotListResponse, BotRow } from "../types";

type Cursor = { beforeID: number };
type BotPageSize = 20 | 50 | 100;
type BotFilter = "verified" | "system" | "user";

// Filters are applied by the server over every bot (and combine with the
// search), so paging walks the filtered set. The key is the query flag.
const FILTERS: { key: BotFilter; label: string }[] = [
  { key: "verified", label: "Verified" },
  { key: "system", label: "System" },
  { key: "user", label: "User-made" }
];

const zeroCursor: Cursor = { beforeID: 0 };

export function BotsPage({ navigate }: { navigate: Navigate }) {
  const [q, setQ] = useState("");
  const [limit, setLimit] = useState<BotPageSize>(50);
  const [data, setData] = useState<BotListResponse | null>(null);
  // history holds the cursor used to reach every page before the current
  // one, so "Previous" can pop back without re-deriving offsets -- keyset
  // pagination has no notion of "page N" to jump back to otherwise.
  const [history, setHistory] = useState<Cursor[]>([]);
  const [cursor, setCursor] = useState<Cursor>(zeroCursor);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [createModalOpen, setCreateModalOpen] = useState(false);
  const [filters, setFilters] = useState<Set<BotFilter>>(new Set());
  // Search runs as the operator types, once the input settles.
  const query = useDebounced(q.trim());

  async function fetchPage(query: string, at: Cursor) {
    setBusy(true);
    setError("");
    const params = new URLSearchParams({ limit: String(limit) });
    if (query.trim()) {
      params.set("q", query.trim());
    }
    for (const f of FILTERS) if (filters.has(f.key)) params.set(f.key, "1");
    if (at.beforeID) {
      params.set("before_id", String(at.beforeID));
    }
    try {
      const result = await api.bots(params);
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
    await fetchPage(query, zeroCursor);
  }

  async function loadNext() {
    if (!data?.has_more) return;
    const at = { beforeID: data.next_before_id };
    const result = await fetchPage(query, at);
    if (result) {
      setHistory((prev) => [...prev, cursor]);
      setCursor(at);
    }
  }

  async function loadPrev() {
    if (history.length === 0) return;
    const at = history[history.length - 1];
    const result = await fetchPage(query, at);
    if (result) {
      setHistory((prev) => prev.slice(0, -1));
      setCursor(at);
    }
  }

  useEffect(() => {
    void loadFresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, limit, filters]);

  const rows = data?.rows ?? [];
  const canGoPrev = history.length > 0 && !busy;
  const canGoNext = Boolean(data?.has_more) && !busy;
  const pageStart = history.length * limit;

  function toggleFilter(key: BotFilter) {
    setFilters((current) => {
      const next = new Set(current);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  const columns: Column<BotRow>[] = [
    {
      key: "name",
      label: "Bot",
      sortValue: (row) => row.FirstName || row.Username,
      render: (row) => (
        <span className="cell-identity">
          <Avatar id={row.ID} firstName={row.FirstName} username={row.Username} size={28} />
          <span>
            <span>{row.FirstName || displayUsername(row.Username) || "—"}</span>
            {row.Username && <span className="cell-sub"> {displayUsername(row.Username)}</span>}
          </span>
        </span>
      )
    },
    { key: "id", label: "ID", className: "mono muted-cell", sortValue: (row) => row.ID, render: (row) => row.ID },
    {
      key: "owner",
      label: "Owner",
      className: "mono",
      sortValue: (row) => row.OwnerUserID,
      render: (row) => (row.OwnerUserID > 0 ? row.OwnerUserID : <span className="muted-cell">—</span>)
    },
    {
      key: "created",
      label: "Created",
      firstDir: "desc",
      sortValue: (row) => new Date(row.CreatedAt).getTime() || 0,
      render: (row) => <span title={formatDate(row.CreatedAt)}>{formatRelative(row.CreatedAt)}</span>
    },
    {
      key: "status",
      label: "Status",
      render: (row) => (
        <span className="cell-badges">
          {row.System && <Badge tone="warn">{"System"}</Badge>}
          {row.Verified && <Badge tone="good">{"Verified"}</Badge>}
          <ScamFakeBadges scam={row.Scam} fake={row.Fake} />
        </span>
      )
    }
  ];

  return (
    <PageFrame
      title={"Bots"}
      actions={
        <>
          <button className="btn primary icon-text" type="button" onClick={() => setCreateModalOpen(true)}>
            <Plus /> {"Create bot"}
          </button>
          <button className="icon-btn" type="button" aria-label={"Refresh"} title={"Refresh"} onClick={() => void loadFresh()} disabled={busy}>
            <ArrowsRotateRight className={busy ? "spin" : undefined} />
          </button>
        </>
      }
    >
      {error && <Alert>{error}</Alert>}
      <div className="list-controls">
        <label className="searchbox">
          <Magnifier />
          <input value={q} onChange={(event) => setQ(event.target.value)} placeholder={"Search by name, username or ID"} aria-label={"Search bots"} />
        </label>
        {FILTERS.map((f) => (
          <FilterChip key={f.key} label={f.label} active={filters.has(f.key)} count={data?.filter_counts?.[f.key]} onToggle={() => toggleFilter(f.key)} />
        ))}
      </div>
      <DataTable
        columns={columns}
        rows={rows}
        rowKey={(row) => row.ID}
        onRowClick={(row) => navigate(`/bots/${row.ID}`)}
        loading={busy && !data}
        empty={query || filters.size > 0 ? "No bots match." : "No bots yet."}
      />
      <div className="table-footer">
        <span>
          {rows.length > 0 ? `${pageStart + 1}–${pageStart + rows.length}` : ""}
          {!query && " · newest first"}
        </span>
        <div className="pager">
          <span>{"Rows"}</span>
          <select value={String(limit)} onChange={(event) => setLimit(Number(event.target.value) as BotPageSize)} aria-label={"Rows per page"}>
            <option value="20">20</option>
            <option value="50">50</option>
            <option value="100">100</option>
          </select>
          <button className="icon-btn" type="button" aria-label={"Previous page"} onClick={() => void loadPrev()} disabled={!canGoPrev}>
            <ChevronLeft />
          </button>
          <button className="icon-btn" type="button" aria-label={"Next page"} onClick={() => void loadNext()} disabled={!canGoNext}>
            <ChevronRight />
          </button>
        </div>
      </div>

      {createModalOpen && (
        <CreateBotModal
          onClose={() => setCreateModalOpen(false)}
          // Deliberately does not close the modal -- the token is only ever
          // shown once, inside ActionButton's own result panel, and closing
          // immediately would yank it away before it can be copied. The
          // operator closes both modals manually once they're done with it.
          onCreated={() => void loadFresh()}
        />
      )}
    </PageFrame>
  );
}
