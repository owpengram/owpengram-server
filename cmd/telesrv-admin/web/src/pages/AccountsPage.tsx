import { ArrowsRotateRight, ChevronLeft, ChevronRight, Magnifier, Smartphone } from "@gravity-ui/icons";
import { useEffect, useMemo, useRef, useState } from "react";
import { api, errorMessage } from "../api";
import { Avatar } from "../components/Avatar";
import { DataTable, FilterChip, type Column } from "../components/DataTable";
import { Alert, Badge, Metric, PageFrame } from "../components/ui";
import { ScamFakeBadges } from "../components/flags";
import { displayName, displayPhone, displayUsername, formatDate, formatRelative, formatUnix } from "../lib/format";
import { useDebounced } from "../lib/useDebounced";
import type { Navigate } from "../routing";
import type { AccountListResponse, AccountRow, AccountStatsResponse } from "../types";

type Cursor = { beforeID: number; beforeActiveUS: number };
type AccountPageSize = 20 | 50 | 100;
type AccountFilter = "premium" | "verified" | "frozen" | "email";

const zeroCursor: Cursor = { beforeID: 0, beforeActiveUS: 0 };

// Filters narrow the page that is loaded; the search box is what asks the
// server. Together they cover "find this person" and "who on this page is X".
const FILTERS: { key: AccountFilter; label: string; test: (row: AccountRow) => boolean }[] = [
  { key: "premium", label: "Premium", test: (row) => row.PremiumUntil > 0 },
  { key: "verified", label: "Verified", test: (row) => row.Verified },
  { key: "frozen", label: "Frozen", test: (row) => row.Frozen },
  { key: "email", label: "Has email", test: (row) => Boolean(row.LoginEmail) }
];

export function AccountsPage({ navigate }: { navigate: Navigate }) {
  const [q, setQ] = useState("");
  const query = useDebounced(q.trim());
  const [limit, setLimit] = useState<AccountPageSize>(50);
  const [data, setData] = useState<AccountListResponse | null>(null);
  const [stats, setStats] = useState<AccountStatsResponse | null>(null);
  // history holds the cursor used to reach every page before the current
  // one, so "Previous" can pop back without re-deriving offsets -- keyset
  // pagination has no notion of "page N" to jump back to otherwise.
  const [history, setHistory] = useState<Cursor[]>([]);
  const [cursor, setCursor] = useState<Cursor>(zeroCursor);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [filters, setFilters] = useState<Set<AccountFilter>>(new Set());
  // Responses can land out of order while typing; only the latest counts.
  const requestSeq = useRef(0);

  async function fetchPage(at: Cursor) {
    const seq = ++requestSeq.current;
    setBusy(true);
    setError("");
    const params = new URLSearchParams({ limit: String(limit) });
    if (query) params.set("q", query);
    if (at.beforeID || at.beforeActiveUS) {
      params.set("before_id", String(at.beforeID));
      params.set("before_active_us", String(at.beforeActiveUS));
    }
    try {
      const result = await api.accounts(params);
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
    const at = { beforeID: data.next_before_id, beforeActiveUS: data.next_before_active_us };
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

  async function loadStats() {
    try {
      setStats(await api.accountStats());
    } catch {
      // Stats are a header nicety; a failure here shouldn't block the list.
    }
  }

  useEffect(() => {
    void loadFresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, limit]);

  useEffect(() => {
    void loadStats();
  }, []);

  function toggleFilter(key: AccountFilter) {
    setFilters((current) => {
      const next = new Set(current);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  const loaded = data?.rows ?? [];
  const rows = useMemo(
    () => loaded.filter((row) => FILTERS.every((f) => !filters.has(f.key) || f.test(row))),
    [loaded, filters]
  );
  const pageStart = history.length * limit;

  const columns: Column<AccountRow>[] = [
    {
      key: "name",
      label: "Account",
      sortValue: (row) => displayName(row),
      render: (row) => (
        <span className="cell-identity">
          <Avatar id={row.ID} firstName={row.FirstName} lastName={row.LastName} username={row.Username} size={28} />
          <span>
            <span>{displayName(row)}</span>
            {row.Username && <span className="cell-sub"> {displayUsername(row.Username)}</span>}
          </span>
        </span>
      )
    },
    { key: "id", label: "ID", className: "mono muted-cell", sortValue: (row) => row.ID, render: (row) => row.ID },
    { key: "phone", label: "Phone", sortValue: (row) => row.Phone, render: (row) => displayPhone(row.Phone) || <span className="muted-cell">—</span> },
    { key: "email", label: "Email", sortValue: (row) => row.LoginEmail, render: (row) => row.LoginEmail || <span className="muted-cell">—</span> },
    {
      key: "active",
      label: "Last active",
      firstDir: "desc",
      sortValue: (row) => new Date(row.LastActiveAt).getTime() || 0,
      render: (row) => <span title={formatDate(row.LastActiveAt)}>{formatRelative(row.LastActiveAt)}</span>
    },
    { key: "devices", label: "Devices", align: "right", firstDir: "desc", sortValue: (row) => row.DeviceCount, render: (row) => row.DeviceCount },
    {
      key: "status",
      label: "Status",
      // Only what sets an account apart is shown; an ordinary account is blank.
      render: (row) => (
        <span className="cell-badges">
          {row.Frozen && <Badge tone="danger">{"Frozen"}</Badge>}
          {row.PremiumUntil > 0 && <span title={`Until ${formatUnix(row.PremiumUntil)}`}><Badge tone="good">{"Premium"}</Badge></span>}
          {row.Verified && <Badge tone="good">{"Verified"}</Badge>}
          <ScamFakeBadges scam={row.Scam} fake={row.Fake} />
        </span>
      )
    }
  ];

  return (
    <PageFrame
      title={"Accounts"}
      actions={
        <>
          <button className="btn icon-text" type="button" onClick={() => navigate("/accounts/shared-devices")}>
            <Smartphone /> {"Shared devices"}
          </button>
          <button
            className="icon-btn"
            type="button"
            aria-label={"Refresh"}
            title={"Refresh"}
            onClick={() => {
              void loadFresh();
              void loadStats();
            }}
            disabled={busy}
          >
            <ArrowsRotateRight className={busy ? "spin" : undefined} />
          </button>
        </>
      }
    >
      {error && <Alert>{error}</Alert>}
      <div className="metric-row">
        <Metric label={"Accounts"} value={stats ? stats.total.toLocaleString() : "…"} loading={!stats} />
        <Metric label={"Online now"} value={stats ? stats.online.toLocaleString() : "…"} tone="good" loading={!stats} />
      </div>
      <div className="list-controls">
        <label className="searchbox">
          <Magnifier />
          <input
            value={q}
            onChange={(event) => setQ(event.target.value)}
            placeholder={"Search by name, username, phone, email or ID"}
            aria-label={"Search accounts"}
          />
        </label>
        {FILTERS.map((f) => (
          <FilterChip
            key={f.key}
            label={f.label}
            active={filters.has(f.key)}
            count={loaded.filter(f.test).length}
            onToggle={() => toggleFilter(f.key)}
          />
        ))}
      </div>
      <DataTable
        columns={columns}
        rows={rows}
        rowKey={(row) => row.ID}
        onRowClick={(row) => navigate(`/accounts/${row.ID}`)}
        loading={busy && !data}
        empty={query || filters.size > 0 ? "No accounts match." : "No accounts yet."}
      />
      <div className="table-footer">
        <span>
          {loaded.length > 0
            ? `${pageStart + 1}–${pageStart + loaded.length}${filters.size > 0 ? ` · ${rows.length} shown` : ""}`
            : ""}
          {!query && " · most recently active first"}
        </span>
        <div className="pager">
          <span>{"Rows"}</span>
          <select value={String(limit)} onChange={(event) => setLimit(Number(event.target.value) as AccountPageSize)} aria-label={"Rows per page"}>
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
