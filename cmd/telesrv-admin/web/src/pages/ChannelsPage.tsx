import { ArrowsRotateRight, ChevronLeft, ChevronRight, Magnifier } from "@gravity-ui/icons";
import { useEffect, useRef, useState } from "react";
import { api, errorMessage } from "../api";
import { Avatar } from "../components/Avatar";
import { DataTable, FilterChip, type Column } from "../components/DataTable";
import { Alert, Badge, PageFrame } from "../components/ui";
import { ScamFakeBadges } from "../components/flags";
import { channelKind, displayUsername, formatDate, formatRelative } from "../lib/format";
import { useDebounced } from "../lib/useDebounced";
import type { Navigate } from "../routing";
import type { ChannelListResponse, ChannelRow } from "../types";

type Cursor = { beforeID: number; beforeUpdatedUS: number };
type ChannelPageSize = 20 | 50 | 100;
type ChannelFilter = "channels" | "groups" | "verified";

const zeroCursor: Cursor = { beforeID: 0, beforeUpdatedUS: 0 };

// Filters are applied by the server over every chat (and combine with the
// search), so paging walks the filtered set. The key is the query flag.
const FILTERS: { key: ChannelFilter; label: string }[] = [
  { key: "channels", label: "Channels" },
  { key: "groups", label: "Groups" },
  { key: "verified", label: "Verified" }
];

export function ChannelsPage({ navigate }: { navigate: Navigate }) {
  const [q, setQ] = useState("");
  const query = useDebounced(q.trim());
  const [limit, setLimit] = useState<ChannelPageSize>(50);
  const [data, setData] = useState<ChannelListResponse | null>(null);
  // history holds the cursor used to reach every page before the current
  // one, so "Previous" can pop back without re-deriving offsets -- keyset
  // pagination has no notion of "page N" to jump back to otherwise.
  const [history, setHistory] = useState<Cursor[]>([]);
  const [cursor, setCursor] = useState<Cursor>(zeroCursor);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [filters, setFilters] = useState<Set<ChannelFilter>>(new Set());
  // Responses can land out of order while typing; only the latest counts.
  const requestSeq = useRef(0);

  async function fetchPage(at: Cursor) {
    const seq = ++requestSeq.current;
    setBusy(true);
    setError("");
    const params = new URLSearchParams({ limit: String(limit) });
    if (query) params.set("q", query);
    for (const f of FILTERS) if (filters.has(f.key)) params.set(f.key, "1");
    if (at.beforeID || at.beforeUpdatedUS) {
      params.set("before_id", String(at.beforeID));
      params.set("before_updated_us", String(at.beforeUpdatedUS));
    }
    try {
      const result = await api.channels(params);
      if (seq !== requestSeq.current) return null;
      setData(result);
      return result;
    } catch (err) {
      if (seq === requestSeq.current) setError(errorMessage(err));
      return null;
    } finally {
      if (seq === requestSeq.current) setBusy(false);
    }
  }

  async function loadFresh() {
    setHistory([]);
    setCursor(zeroCursor);
    await fetchPage(zeroCursor);
  }

  async function loadNext() {
    if (!data?.has_more) return;
    const at = { beforeID: data.next_before_id, beforeUpdatedUS: data.next_before_updated_us };
    if (await fetchPage(at)) {
      setHistory((prev) => [...prev, cursor]);
      setCursor(at);
    }
  }

  async function loadPrev() {
    if (history.length === 0) return;
    const at = history[history.length - 1];
    if (await fetchPage(at)) {
      setHistory((prev) => prev.slice(0, -1));
      setCursor(at);
    }
  }

  useEffect(() => {
    void loadFresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, limit, filters]);

  function toggleFilter(key: ChannelFilter) {
    setFilters((current) => {
      const next = new Set(current);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  const rows = data?.rows ?? [];
  const pageStart = history.length * limit;

  const columns: Column<ChannelRow>[] = [
    {
      key: "title",
      label: "Name",
      sortValue: (row) => row.Title,
      render: (row) => (
        <span className="cell-identity">
          <Avatar id={row.ID} kind="channel" title={row.Title} size={28} />
          <span>
            <span>{row.Title || "—"}</span>
            {row.Username && <span className="cell-sub"> {displayUsername(row.Username)}</span>}
          </span>
        </span>
      )
    },
    { key: "kind", label: "Kind", sortValue: (row) => channelKind(row), render: (row) => channelKind(row) },
    { key: "id", label: "ID", className: "mono muted-cell", sortValue: (row) => row.ID, render: (row) => row.ID },
    { key: "members", label: "Members", align: "right", firstDir: "desc", sortValue: (row) => row.ParticipantsCount, render: (row) => row.ParticipantsCount.toLocaleString() },
    { key: "admins", label: "Admins", align: "right", firstDir: "desc", sortValue: (row) => row.AdminsCount, render: (row) => row.AdminsCount },
    {
      key: "updated",
      label: "Updated",
      firstDir: "desc",
      sortValue: (row) => new Date(row.UpdatedAt).getTime() || 0,
      render: (row) => <span title={formatDate(row.UpdatedAt)}>{formatRelative(row.UpdatedAt)}</span>
    },
    {
      key: "status",
      label: "Status",
      render: (row) => (
        <span className="cell-badges">
          {row.Verified && <Badge tone="good">{"Verified"}</Badge>}
          <ScamFakeBadges scam={row.Scam} fake={row.Fake} />
        </span>
      )
    }
  ];

  return (
    <PageFrame
      title={"Groups and channels"}
      actions={
        <button className="icon-btn" type="button" aria-label={"Refresh"} title={"Refresh"} onClick={() => void loadFresh()} disabled={busy}>
          <ArrowsRotateRight className={busy ? "spin" : undefined} />
        </button>
      }
    >
      {error && <Alert>{error}</Alert>}
      <div className="list-controls">
        <label className="searchbox">
          <Magnifier />
          <input value={q} onChange={(event) => setQ(event.target.value)} placeholder={"Search by title, username or ID"} aria-label={"Search groups and channels"} />
        </label>
        {FILTERS.map((f) => (
          <FilterChip key={f.key} label={f.label} active={filters.has(f.key)} count={data?.filter_counts?.[f.key]} onToggle={() => toggleFilter(f.key)} />
        ))}
      </div>
      <DataTable
        columns={columns}
        rows={rows}
        rowKey={(row) => row.ID}
        onRowClick={(row) => navigate(`/channels/${row.ID}`)}
        loading={busy && !data}
        empty={query || filters.size > 0 ? "Nothing matches." : "No groups or channels yet."}
      />
      <div className="table-footer">
        <span>
          {rows.length > 0 ? `${pageStart + 1}–${pageStart + rows.length}` : ""}
          {!query && " · most recently updated first"}
        </span>
        <div className="pager">
          <span>{"Rows"}</span>
          <select value={String(limit)} onChange={(event) => setLimit(Number(event.target.value) as ChannelPageSize)} aria-label={"Rows per page"}>
            <option value="20">20</option>
            <option value="50">50</option>
            <option value="100">100</option>
          </select>
          <button className="icon-btn" type="button" aria-label={"Previous page"} onClick={() => void loadPrev()} disabled={history.length === 0 || busy}>
            <ChevronLeft />
          </button>
          <button className="icon-btn" type="button" aria-label={"Next page"} onClick={() => void loadNext()} disabled={!data?.has_more || busy}>
            <ChevronRight />
          </button>
        </div>
      </div>
    </PageFrame>
  );
}
