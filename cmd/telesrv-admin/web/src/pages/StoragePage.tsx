import { ArrowsRotateRight, Magnifier } from "@gravity-ui/icons";
import { X } from "../components/icons";
import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { api, errorMessage } from "../api";
import { cacheGet, cacheKeys, cacheSet } from "../lib/cache";
import { ActionButton } from "../components/ActionButton";
import { Avatar } from "../components/Avatar";
import { DataTable, type Column, type SortState } from "../components/DataTable";
import { StorageSettings, isStorageGroup } from "../components/StorageSettings";
import { Alert, LoadingSurface, Metric, PageFrame } from "../components/ui";
import { displayUsername, formatBytes, formatQuantity } from "../lib/format";
import { useDebounced } from "../lib/useDebounced";
import type { Navigate } from "../routing";
import type { AccountStorageRow, EnvGroup, StorageStatsResponse } from "../types";

type StorageSortKey = "user_id" | "username" | "bytes" | "files";

function StorageOverviewTab({ navigate }: { navigate: Navigate }) {
  // Seeded from this session's last figures, so returning to Storage opens on
  // numbers rather than on shimmering placeholders. loadStats still runs.
  const [stats, setStats] = useState<StorageStatsResponse | null>(
    () => cacheGet<StorageStatsResponse>(cacheKeys.storageStats) ?? null
  );
  // Tracked separately from `error`: loadStats deliberately swallows its
  // failure so it can't block the account list, which would otherwise leave
  // the metric skeletons shimmering forever on a stats-only outage.
  const [statsFailed, setStatsFailed] = useState(false);
  const [rows, setRows] = useState<AccountStorageRow[]>(
    () => cacheGet<AccountStorageRow[]>(cacheKeys.storageAccounts) ?? []
  );
  const [hasMore, setHasMore] = useState(false);
  const [offset, setOffset] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [q, setQ] = useState("");
  const query = useDebounced(q.trim());
  // The server sorts this list (it is paged), so the table only reports clicks.
  const [sort, setSort] = useState<SortState>({ key: "bytes", dir: "desc" });

  async function loadStats() {
    try {
      const next = await api.storageStats();
      cacheSet(cacheKeys.storageStats, next);
      setStats(next);
      setStatsFailed(false);
    } catch {
      // Stats are a header nicety; a failure here shouldn't block the list.
      setStatsFailed(true);
    }
  }

  async function loadAccounts(next = false) {
    setBusy(true);
    setError("");
    const at = next ? offset : 0;
    const params = new URLSearchParams({
      limit: "50",
      offset: String(at),
      sort: sort.key,
      order: sort.dir
    });
    if (query) params.set("q", query);
    try {
      const result = await api.storageAccounts(params);
      const page = result.rows ?? [];
      setRows((current) => {
        const merged = next ? [...current, ...page] : page;
        // Only the first page is worth keeping: it is what the screen opens on,
        // and caching an appended list would restore a scroll position nobody
        // asked for.
        if (!next && !query) {
          cacheSet(cacheKeys.storageAccounts, merged);
        }
        return merged;
      });
      setOffset(result.next_offset);
      setHasMore(Boolean(result.has_more));
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  function refresh() {
    void loadStats();
    void loadAccounts(false);
  }

  useEffect(() => {
    void loadStats();
  }, []);

  useEffect(() => {
    void loadAccounts(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sort, query]);

  // Physical is what actually consumes disk/S3 (deduplicated); logical is the
  // sum of what the per-account table below adds up to. They legitimately
  // differ when the same content is shared by more than one document/photo.
  const dedupBytes = stats ? Math.max(0, Number(stats.LogicalBytes) - Number(stats.PhysicalBytes)) : 0;
  const statsLoading = !stats && !statsFailed;
  // Bars are relative to the largest account loaded, so the heaviest users
  // stand out at a glance without reading every number.
  const maxBytes = rows.reduce((max, row) => Math.max(max, Number(row.Bytes) || 0), 0);

  const columns: Column<AccountStorageRow>[] = [
    {
      key: "username",
      label: "Account",
      sortValue: () => 0,
      render: (row) => (
        <span className="cell-identity">
          <Avatar id={Number(row.UserID)} firstName={row.FirstName} username={row.Username} size={28} />
          <span>
            <span>{row.FirstName || displayUsername(row.Username) || "—"}</span>
            {row.Username && row.FirstName && <span className="cell-sub"> {displayUsername(row.Username)}</span>}
          </span>
        </span>
      )
    },
    { key: "user_id", label: "ID", className: "mono muted-cell", sortValue: () => 0, render: (row) => row.UserID },
    {
      key: "bytes",
      label: "Storage used",
      firstDir: "desc",
      sortValue: () => 0,
      render: (row) => (
        <>
          <span className="mono">{formatBytes(row.Bytes)}</span>
          <span className="cell-bar" aria-hidden="true">
            <i style={{ width: `${maxBytes > 0 ? Math.max(2, Math.round((Number(row.Bytes) / maxBytes) * 100)) : 0}%` }} />
          </span>
        </>
      )
    },
    { key: "files", label: "Files", align: "right", firstDir: "desc", sortValue: () => 0, render: (row) => formatQuantity(row.FileCount) }
  ];

  return (
    <>
      {error && <Alert>{error}</Alert>}
      <div className="metric-row">
        <Metric label={"On disk"} value={stats ? formatBytes(stats.PhysicalBytes) : "-"} loading={statsLoading} />
        <Metric
          label={"Saved by dedup"}
          // Derived from stats, so before they land this is a placeholder 0 --
          // it has to shimmer like the rest rather than assert "0 B", which
          // reads as a real measurement of nothing saved.
          value={formatBytes(String(dedupBytes))}
          loading={statsLoading}
          tone={dedupBytes > 0 ? "good" : "neutral"}
        />
        <Metric label={"Photos"} value={stats ? formatQuantity(stats.PhotoCount) : "-"} loading={statsLoading} />
        <Metric label={"Documents"} value={stats ? formatQuantity(stats.DocumentCount) : "-"} loading={statsLoading} />
        <Metric label={"System content"} value={stats ? formatBytes(stats.SystemBytes) : "-"} loading={statsLoading} />
        <Metric label={"Backend"} value={stats?.BackendKind ?? "-"} loading={statsLoading} />
      </div>

      <div className="list-controls">
        <label className="searchbox">
          <Magnifier />
          <input
            value={q}
            onChange={(event) => setQ(event.target.value)}
            placeholder={"Search by name, username or ID"}
            aria-label={"Search accounts"}
          />
        </label>
        <span className="list-controls-spacer" />
        <button className="icon-btn" type="button" onClick={refresh} disabled={busy} aria-label={"Refresh"} title={"Refresh"}>
          <ArrowsRotateRight className={busy ? "spin" : undefined} />
        </button>
      </div>

      <DataTable
        columns={columns}
        rows={rows}
        rowKey={(row) => row.UserID}
        onRowClick={(row) => navigate(`/accounts/${row.UserID}`)}
        sort={sort}
        onSort={setSort}
        loading={busy && rows.length === 0}
        empty={query ? "No accounts match." : "No account has stored any media yet."}
      />
      <div className="table-footer">
        <span>
          {rows.length > 0 && (stats ? `${rows.length} of ${formatQuantity(stats.AccountCount)} accounts with media` : `${rows.length} accounts`)}
        </span>
        {hasMore && (
          <button className="btn" type="button" onClick={() => void loadAccounts(true)} disabled={busy}>
            {"Load more"}
          </button>
        )}
      </div>
    </>
  );
}

export function StoragePage({ navigate }: { navigate: Navigate }) {
  const [tab, setTab] = useState<"overview" | "limits">("overview");
  return (
    <PageFrame title={"Storage"}>
      <div className="tab-bar" role="tablist" aria-label={"Storage sections"}>
        <button className={`tab-btn ${tab === "overview" ? "active" : ""}`} type="button" role="tab" aria-selected={tab === "overview"} onClick={() => setTab("overview")}>
          {"Usage"}
        </button>
        <button className={`tab-btn ${tab === "limits" ? "active" : ""}`} type="button" role="tab" aria-selected={tab === "limits"} onClick={() => setTab("limits")}>
          {"Limits and retention"}
        </button>
      </div>
      {tab === "overview" ? <StorageOverviewTab navigate={navigate} /> : <LimitsRetentionSection />}
    </PageFrame>
  );
}

// --- Limits & Retention --------------------------------------------------
//
// The tab draws the "Storage & Media" .env group with StorageSettings, the
// same panel as Settings > Storage & Media. The pieces below are shared with
// it: the per-type retention keys, the protocol's file-size ceiling, duration
// parsing and the manual purge dialog.

function LimitsRetentionSection() {
  const [group, setGroup] = useState<EnvGroup | null>(null);
  const [error, setError] = useState("");

  async function load() {
    setError("");
    try {
      const groups = await api.serverEnv();
      setGroup(groups.find(isStorageGroup) ?? null);
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  useEffect(() => { void load(); }, []);

  if (error) return <Alert>{error}</Alert>;
  if (!group) return <LoadingSurface label={"Loading storage settings..."} />;
  return <StorageSettings group={group} onSaved={() => void load()} />;
}

// Per-category retention age overrides -- each optional, empty/0 means
// "inherit the shared Retention age" above. Order matches the admin UI list.
export const CATEGORY_AGE_FIELDS: { key: string; envKey: string; label: string }[] = [
  { key: "photo", envKey: "TELESRV_STORAGE_RETENTION_MAX_AGE_PHOTO", label: "Photo" },
  { key: "video", envKey: "TELESRV_STORAGE_RETENTION_MAX_AGE_VIDEO", label: "Video" },
  { key: "round_video", envKey: "TELESRV_STORAGE_RETENTION_MAX_AGE_ROUND_VIDEO", label: "Round video (video message)" },
  { key: "gif", envKey: "TELESRV_STORAGE_RETENTION_MAX_AGE_GIF", label: "GIF" },
  { key: "music", envKey: "TELESRV_STORAGE_RETENTION_MAX_AGE_MUSIC", label: "Music" },
  { key: "voice", envKey: "TELESRV_STORAGE_RETENTION_MAX_AGE_VOICE", label: "Voice message" },
  { key: "file", envKey: "TELESRV_STORAGE_RETENTION_MAX_AGE_FILE", label: "File" },
  { key: "avatar", envKey: "TELESRV_STORAGE_RETENTION_MAX_AGE_AVATAR", label: "Avatar" }
];

// DANGER_ZONE_CATEGORY_KEYS marks the two CATEGORY_AGE_FIELDS/manual-purge
// categories that reach beyond ordinary per-user media: Avatar also covers
// the built-in system bots' own profile photos, and GIF also covers the
// server's bundled GIF catalog (@gif). ManualPurgeStorageModal splits these into their own boxed "Danger
// zone" section instead of listing them alongside Photo/Video/Music/etc.
export const DANGER_ZONE_CATEGORY_KEYS = new Set(["gif", "avatar"]);

// Mirrors internal/app/files.MaxUploadPartBytes * MaxUploadParts -- the
// protocol's own upload-part-count ceiling that TELESRV_STORAGE_MAX_UPLOAD_FILE_BYTES
// can never legally exceed (internal/config validates this server-side too;
// this is just an early, friendlier warning in the form).
export const PROTOCOL_UPLOAD_CEILING_BYTES = 524288 * 8000;

// parseDurationMinutes reads a Go-style duration string (e.g. "720h", "90m",
// "1h30m") and returns the total as minutes. Unrecognized/empty input is 0.
export function parseDurationMinutes(value: string): number {
  let totalSeconds = 0;
  const re = /(\d+(?:\.\d+)?)\s*(h|m|s)/g;
  let match: RegExpExecArray | null;
  while ((match = re.exec(value)) !== null) {
    const amount = parseFloat(match[1]);
    const unit = match[2];
    totalSeconds += unit === "h" ? amount * 3600 : unit === "m" ? amount * 60 : amount;
  }
  return totalSeconds / 60;
}

// ManualPurgeStorageModal lets an operator delete media blob bytes right now
// by hand-picked category (and, optionally, avatars) with an optional
// "created before" age cutoff -- the manual counterpart of the automatic
// hard-retention sweep above. Leaving the date empty purges everything
// matching the selected categories regardless of age. Deletion semantics are
// identical to "hard" retention mode: only the file bytes are removed, never
// the document/photo row, so an affected message still renders its
// placeholder (see internal/app/files.Service.ManualPurge's doc comment).
// Hands the actual mutation off to ActionButton
// (reason + dry-run + confirm) since -- unlike the settings above -- this is
// an immediate, irreversible delete rather than a saved .env value.
export function ManualPurgeStorageModal({ onClose }: { onClose: () => void }) {
  const [selected, setSelected] = useState<Record<string, boolean>>({});
  const [dateValue, setDateValue] = useState("");

  const documentFields = CATEGORY_AGE_FIELDS.filter((f) => f.key !== "avatar");
  const allSelected = CATEGORY_AGE_FIELDS.every((f) => selected[f.key]);

  function toggle(key: string) {
    setSelected((prev) => ({ ...prev, [key]: !prev[key] }));
  }

  function toggleAll() {
    const next = !allSelected;
    const nextSelected: Record<string, boolean> = {};
    for (const field of CATEGORY_AGE_FIELDS) nextSelected[field.key] = next;
    setSelected(nextSelected);
  }

  const chosenCategories = documentFields.filter((f) => selected[f.key]).map((f) => f.key);
  const includeAvatars = Boolean(selected.avatar);
  const canSubmit = chosenCategories.length > 0 || includeAvatars;

  return createPortal(
    <div className="modal-backdrop" role="presentation">
      <section className="modal command-modal" role="dialog" aria-modal="true" aria-label={"Manually purge storage"}>
        <div className="modal-head">
          <div>
            <div className="eyebrow">{"Limits & Retention"}</div>
            <h2>{"Manually purge storage"}</h2>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} aria-label={"Close"}><X size={15} /></button>
        </div>
        <div className="command-body">
          <p className="env-field-desc">
            {"Deletes the file bytes of every document/photo matching the categories below, right now -- independent of the retention mode/age configured above. The message/profile-photo itself is never deleted, only its file; a purged item starts showing as unavailable. Leave \"Created before\" empty to purge everything in the selected categories, regardless of age."}
          </p>
          <div className="attr-block">
            <label className="checkline">
              <input type="checkbox" checked={allSelected} onChange={toggleAll} />
              {" "}{"Select all"}
            </label>
            {CATEGORY_AGE_FIELDS.filter((f) => !DANGER_ZONE_CATEGORY_KEYS.has(f.key)).map((field) => (
              <label key={field.key} className="checkline">
                <input type="checkbox" checked={Boolean(selected[field.key])} onChange={() => toggle(field.key)} />
                {" "}{field.label}
              </label>
            ))}
          </div>
          <div className="danger-zone-box">
            <div className="danger-zone-box-title">{"Danger zone"}</div>
            <div className="danger-zone-box-body">
              <p className="env-field-desc">
                {"Avatar also purges system bot avatars (auto-restored on next restart). GIF also purges the bundled GIF catalog (@gif) -- permanently, unless its source file is still in TELESRV_GIF_SEED_DIR."}
              </p>
              {CATEGORY_AGE_FIELDS.filter((f) => DANGER_ZONE_CATEGORY_KEYS.has(f.key)).map((field) => (
                <label key={field.key} className="checkline">
                  <input type="checkbox" checked={Boolean(selected[field.key])} onChange={() => toggle(field.key)} />
                  {" "}{field.label}
                </label>
              ))}
            </div>
          </div>
          <label className="duration-field">
            <span>{"Created before (optional)"}</span>
            <input type="date" value={dateValue} onChange={(event) => setDateValue(event.target.value)} />
            <span className="env-field-desc">{"Empty = no age limit, purge everything matching the selected categories."}</span>
          </label>
        </div>
        <div className="modal-actions">
          <button className="btn" type="button" onClick={onClose}>{"Close"}</button>
          <ActionButton
            disabled={!canSubmit}
            label={"Purge selected storage"}
            tone="danger"
            path="/api/actions/storage-manual-purge"
            payload={() => ({
              categories: chosenCategories,
              include_avatars: includeAvatars,
              created_before: dateValue ? new Date(dateValue).toISOString() : undefined
            })}
          />
        </div>
      </section>
    </div>,
    document.body
  );
}
