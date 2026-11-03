import { ArrowsRotateRight, Magnifier } from "@gravity-ui/icons";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import { DataTable, type Column } from "../components/DataTable";
import { Alert, Badge, Metric, PageFrame } from "../components/ui";
import { displayUsername, formatDate, formatQuantity, formatRelative, toNumeric } from "../lib/format";
import { useDebounced } from "../lib/useDebounced";
import type { Navigate } from "../routing";
import type { AccountRatingRow } from "../types";

export function AccountRatingsPage({ navigate }: { navigate: Navigate }) {
  const [minLevel, setMinLevel] = useState("");
  const [search, setSearch] = useState("");
  // One free-text field: the backend matches a username prefix (editable or
  // collectible), a first/last name prefix, and a bare number as the user id.
  const query = useDebounced(search.trim());
  const level = useDebounced(minLevel.trim());
  const [rows, setRows] = useState<AccountRatingRow[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [cursor, setCursor] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function load(next = false) {
    setBusy(true);
    setError("");
    const params = new URLSearchParams({ limit: "50" });
    if (level) params.set("min_level", level);
    if (query) params.set("q", query);
    if (next && cursor) params.set("before_id", cursor);
    try {
      const result = await api.accountRatings(params);
      const page = result.rows ?? [];
      setRows((current) => (next ? [...current, ...page] : page));
      setCursor(result.next_before_id ?? "");
      setHasMore(Boolean(result.has_more));
      setLoaded(true);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void load(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, level]);

  const topLevel = rows.reduce((max, row) => Math.max(max, row.Level), 0);
  const pendingCount = rows.filter((row) => toNumeric(row.PendingStars) !== 0).length;
  const avgLevel = rows.length > 0 ? (rows.reduce((sum, row) => sum + row.Level, 0) / rows.length).toFixed(1) : "0";

  const columns: Column<AccountRatingRow>[] = [
    {
      key: "user",
      label: "Account",
      sortValue: (row) => displayUsername(row.Username) || row.FirstName,
      render: (row) => (
        <span>
          <span>{row.FirstName || displayUsername(row.Username) || "—"}</span>
          {row.Username && row.FirstName && <span className="cell-sub"> {displayUsername(row.Username)}</span>}
        </span>
      )
    },
    { key: "id", label: "ID", className: "mono muted-cell", sortValue: (row) => Number(row.UserID), render: (row) => row.UserID },
    { key: "level", label: "Level", firstDir: "desc", sortValue: (row) => row.Level, render: (row) => <LevelBadge level={row.Level} /> },
    { key: "stars", label: "Stars", align: "right", firstDir: "desc", sortValue: (row) => toNumeric(row.Stars), render: (row) => formatQuantity(row.Stars) },
    { key: "progress", label: "Progress", render: (row) => <RatingProgress row={row} /> },
    {
      key: "pending",
      label: "Pending",
      align: "right",
      firstDir: "desc",
      sortValue: (row) => toNumeric(row.PendingStars),
      render: (row) => (toNumeric(row.PendingStars) !== 0 ? formatQuantity(row.PendingStars) : "")
    },
    {
      key: "computed",
      label: "Computed",
      firstDir: "desc",
      sortValue: (row) => new Date(row.ComputedAt).getTime() || 0,
      render: (row) => <span title={formatDate(row.ComputedAt)}>{formatRelative(row.ComputedAt)}</span>
    }
  ];

  return (
    <PageFrame
      title="Ratings"
      actions={
        <button className="icon-btn" type="button" aria-label="Refresh" title="Refresh" onClick={() => void load(false)} disabled={busy}>
          <ArrowsRotateRight className={busy ? "spin" : undefined} />
        </button>
      }
    >
      {error && <Alert>{error}</Alert>}
      <div className="metric-row">
        <Metric label="Top level" value={String(topLevel)} tone="good" loading={!loaded} />
        <Metric label="Average level" value={avgLevel} loading={!loaded} />
        <Metric label="With pending stars" value={String(pendingCount)} tone={pendingCount ? "warn" : "neutral"} loading={!loaded} />
      </div>
      <div className="list-controls">
        <label className="searchbox">
          <Magnifier />
          <input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Search by name, username or ID" aria-label="Search ratings" />
        </label>
        <label className="searchbox" style={{ maxWidth: 160 }}>
          <span className="muted-cell">Level ≥</span>
          <input value={minLevel} onChange={(event) => setMinLevel(event.target.value)} type="number" min="0" placeholder="0" aria-label="Minimum level" />
        </label>
      </div>
      <DataTable
        columns={columns}
        rows={rows}
        rowKey={(row) => row.UserID}
        onRowClick={(row) => navigate(`/account-ratings/${row.UserID}`)}
        loading={busy && !loaded}
        empty={query || level ? "No accounts match." : "No ratings computed yet."}
      />
      <div className="table-footer">
        <span>{rows.length > 0 ? `${rows.length} shown` : ""}</span>
        {hasMore && (
          <button className="btn" type="button" onClick={() => void load(true)} disabled={busy}>
            Load more
          </button>
        )}
      </div>
    </PageFrame>
  );
}

export function LevelBadge({ level }: { level: number }) {
  const tone = level >= 10 ? "good" : level >= 5 ? "warn" : "neutral";
  return <Badge tone={tone}>{`Level ${level}`}</Badge>;
}

export function levelProgress(row: AccountRatingRow): { percent: number; remaining: number; target: number; stars: number } {
  const stars = toNumeric(row.Stars);
  const current = toNumeric(row.CurrentLevelStars);
  const target = toNumeric(row.NextLevelStars);
  const span = target - current;
  const percent = span > 0 ? Math.min(100, Math.max(0, ((stars - current) / span) * 100)) : 0;
  return { percent, remaining: Math.max(0, target - stars), target, stars };
}

export function RatingProgress({ row }: { row: AccountRatingRow }) {
  if (!row.HasNextLevel) {
    return <span className="progress-note">Max level</span>;
  }
  const { percent, remaining, target } = levelProgress(row);
  return (
    <div className="progress-cell">
      <div className="progress-bar" role="img" aria-label={`${Math.round(percent)}%`}>
        <span style={{ width: `${percent}%` }} />
      </div>
      <small>{`${formatQuantity(String(remaining))} to go (${formatQuantity(String(target))} total)`}</small>
    </div>
  );
}
