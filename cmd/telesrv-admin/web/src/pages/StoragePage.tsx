import { ArrowDown, ArrowUp, ArrowUpDown, ChevronDown, ChevronRight, Loader2, RefreshCw, Search, Settings2, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { createPortal } from "react-dom";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { Alert, EmptyRow, Metric, PageFrame, QueryPanel, SectionHead } from "../components/ui";
import { displayUsername, formatBytes, formatQuantity } from "../lib/format";
import type { Navigate } from "../routing";
import type { AccountStorageRow, StorageStatsResponse } from "../types";

type StorageSortKey = "user_id" | "username" | "bytes" | "files";

// SortableHeader is a <th> that toggles ascending/descending on click and
// shows which column (and direction) is currently active -- there's no
// existing sortable-table convention elsewhere in this admin panel to
// mirror, so this is a small, self-contained one for this page.
function SortableHeader({
  label,
  sortKey,
  activeKey,
  desc,
  onSort
}: {
  label: string;
  sortKey: StorageSortKey;
  activeKey: StorageSortKey;
  desc: boolean;
  onSort: (key: StorageSortKey) => void;
}) {
  const active = sortKey === activeKey;
  return (
    <th>
      <button type="button" className="sort-header" onClick={() => onSort(sortKey)}>
        {label}
        {active ? (desc ? <ArrowDown size={13} /> : <ArrowUp size={13} />) : <ArrowUpDown size={13} className="sort-header-idle" />}
      </button>
    </th>
  );
}

function StorageOverviewTab({ navigate }: { navigate: Navigate }) {
  const [stats, setStats] = useState<StorageStatsResponse | null>(null);
  const [rows, setRows] = useState<AccountStorageRow[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [offset, setOffset] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [q, setQ] = useState("");
  const [sortKey, setSortKey] = useState<StorageSortKey>("bytes");
  const [sortDesc, setSortDesc] = useState(true);

  async function loadStats() {
    try {
      setStats(await api.storageStats());
    } catch {
      // Stats are a header nicety; a failure here shouldn't block the list.
    }
  }

  async function loadAccounts(next = false) {
    setBusy(true);
    setError("");
    const at = next ? offset : 0;
    const params = new URLSearchParams({
      limit: "50",
      offset: String(at),
      sort: sortKey,
      order: sortDesc ? "desc" : "asc"
    });
    if (q.trim()) params.set("q", q.trim());
    try {
      const result = await api.storageAccounts(params);
      const page = result.rows ?? [];
      setRows((current) => (next ? [...current, ...page] : page));
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
    refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sortKey, sortDesc]);

  function toggleSort(key: StorageSortKey) {
    if (key === sortKey) {
      setSortDesc((current) => !current);
    } else {
      setSortKey(key);
      setSortDesc(true);
    }
  }

  // Physical is what actually consumes disk/S3 (deduplicated); logical is the
  // sum of what the per-account table below adds up to. They legitimately
  // differ when the same content is shared by more than one document/photo.
  const dedupBytes = stats ? Math.max(0, Number(stats.LogicalBytes) - Number(stats.PhysicalBytes)) : 0;

  return (
    <>
      {error && <Alert>{error}</Alert>}
      <div className="metric-row">
        <Metric label={"Physical usage (on disk / S3)"} value={stats ? formatBytes(stats.PhysicalBytes) : "-"} />
        <Metric label={"Logical usage (sum per account)"} value={stats ? formatBytes(stats.LogicalBytes) : "-"} />
        <Metric label={"Saved by dedup"} value={formatBytes(String(dedupBytes))} tone={dedupBytes > 0 ? "good" : "neutral"} />
        <Metric label={"Backend"} value={stats?.BackendKind ?? "-"} />
      </div>
      <div className="metric-row">
        <Metric label={"Documents"} value={stats ? formatQuantity(stats.DocumentCount) : "-"} />
        <Metric label={"Photos"} value={stats ? formatQuantity(stats.PhotoCount) : "-"} />
        <Metric label={"Accounts with media"} value={stats ? formatQuantity(stats.AccountCount) : "-"} />
        <Metric
          label={"Unattributed"}
          value={stats ? formatBytes(stats.UnattributedBytes) : "-"}
          tone={stats && Number(stats.UnattributedBytes) > 0 ? "warn" : "neutral"}
        />
      </div>

      <QueryPanel>
        <form className="toolbar" onSubmit={(event) => { event.preventDefault(); void loadAccounts(false); }}>
          <label className="searchbox">
            <Search size={15} />
            <input value={q} onChange={(event) => setQ(event.target.value)} placeholder={"User ID / username / name"} />
          </label>
          <button className="btn primary icon-text" type="submit" disabled={busy}>
            {busy ? <Loader2 size={15} className="spin" /> : <Search size={15} />} {"Search"}
          </button>
          <button className="btn icon-text" type="button" onClick={refresh} disabled={busy}>
            <RefreshCw size={15} className={busy ? "spin" : ""} /> {"Refresh"}
          </button>
        </form>
      </QueryPanel>

      <div className="table-wrap">
        <table className="data-table">
          <thead>
            <tr>
              <SortableHeader label={"User ID"} sortKey="user_id" activeKey={sortKey} desc={sortDesc} onSort={toggleSort} />
              <SortableHeader label={"Account"} sortKey="username" activeKey={sortKey} desc={sortDesc} onSort={toggleSort} />
              <SortableHeader label={"Storage used"} sortKey="bytes" activeKey={sortKey} desc={sortDesc} onSort={toggleSort} />
              <SortableHeader label={"Files"} sortKey="files" activeKey={sortKey} desc={sortDesc} onSort={toggleSort} />
              <th></th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.UserID}>
                <td className="mono">{row.UserID}</td>
                <td>{displayUsername(row.Username) || row.FirstName || "-"}</td>
                <td className="mono">{formatBytes(row.Bytes)}</td>
                <td className="mono">{formatQuantity(row.FileCount)}</td>
                <td><button className="row-link" type="button" onClick={() => navigate(`/accounts/${row.UserID}`)}>{"Details"} <ChevronRight size={14} /></button></td>
              </tr>
            ))}
            {rows.length === 0 && <EmptyRow colSpan={5} />}
          </tbody>
        </table>
      </div>
      {hasMore && (
        <div className="toolbar">
          <button className="btn icon-text" type="button" onClick={() => loadAccounts(true)} disabled={busy}>
            {busy ? <Loader2 size={15} className="spin" /> : <ChevronDown size={15} />} {"Load more"}
          </button>
        </div>
      )}
    </>
  );
}

export function StoragePage({ navigate }: { navigate: Navigate }) {
  const [tab, setTab] = useState<"overview" | "limits">("overview");
  return (
    <PageFrame title={"Storage"} eyebrow={"Media / Storage usage"}>
      <div className="tab-bar" role="tablist" aria-label={"Storage sections"}>
        <button className={`tab-btn ${tab === "overview" ? "active" : ""}`} type="button" role="tab" aria-selected={tab === "overview"} onClick={() => setTab("overview")}>
          {"Overview"}
        </button>
        <button className={`tab-btn ${tab === "limits" ? "active" : ""}`} type="button" role="tab" aria-selected={tab === "limits"} onClick={() => setTab("limits")}>
          {"Limits & Retention"}
        </button>
      </div>
      {tab === "overview" ? <StorageOverviewTab navigate={navigate} /> : <LimitsRetentionSection />}
    </PageFrame>
  );
}

// --- Limits & Retention --------------------------------------------------
//
// Edits a handful of TELESRV_STORAGE_* keys through the existing generic
// .env editor endpoints (GET /api/server/env, POST
// /api/actions/update-server-env -- see cmd/telesrv-admin/serversettings.go
// and ServerSettingsPage.tsx's EnvSection, which this mirrors for its
// save/reload flow), but with friendly units instead of raw key=value text:
// GB inputs for byte budgets, a mode dropdown + day count for retention.

const STORAGE_ENV_KEYS = {
  blobBackend: "TELESRV_BLOB_BACKEND",
  maxTotal: "TELESRV_STORAGE_MAX_TOTAL_BYTES",
  minFree: "TELESRV_STORAGE_MIN_FREE_BYTES",
  maxUploadFile: "TELESRV_STORAGE_MAX_UPLOAD_FILE_BYTES",
  retentionMode: "TELESRV_STORAGE_RETENTION_MODE",
  retentionMaxAge: "TELESRV_STORAGE_RETENTION_MAX_AGE",
  evictionEnable: "TELESRV_STORAGE_EVICTION_ENABLE"
} as const;

// Per-category retention age overrides -- each optional, empty/0 means
// "inherit the shared Retention age" above. Order matches the admin UI list.
const CATEGORY_AGE_FIELDS: { key: string; envKey: string; label: string }[] = [
  { key: "photo", envKey: "TELESRV_STORAGE_RETENTION_MAX_AGE_PHOTO", label: "Photo" },
  { key: "video", envKey: "TELESRV_STORAGE_RETENTION_MAX_AGE_VIDEO", label: "Video" },
  { key: "round_video", envKey: "TELESRV_STORAGE_RETENTION_MAX_AGE_ROUND_VIDEO", label: "Round video (video message)" },
  { key: "gif", envKey: "TELESRV_STORAGE_RETENTION_MAX_AGE_GIF", label: "GIF" },
  { key: "music", envKey: "TELESRV_STORAGE_RETENTION_MAX_AGE_MUSIC", label: "Music" },
  { key: "voice", envKey: "TELESRV_STORAGE_RETENTION_MAX_AGE_VOICE", label: "Voice message" },
  { key: "file", envKey: "TELESRV_STORAGE_RETENTION_MAX_AGE_FILE", label: "File" },
  { key: "avatar", envKey: "TELESRV_STORAGE_RETENTION_MAX_AGE_AVATAR", label: "Avatar" }
];

// Mirrors internal/app/files.MaxUploadPartBytes * MaxUploadParts -- the
// protocol's own upload-part-count ceiling that TELESRV_STORAGE_MAX_UPLOAD_FILE_BYTES
// can never legally exceed (internal/config validates this server-side too;
// this is just an early, friendlier warning in the form).
const PROTOCOL_UPLOAD_CEILING_BYTES = 524288 * 8000;

const BYTE_UNITS: { label: string; bytes: number }[] = [
  { label: "MB", bytes: 1024 ** 2 },
  { label: "GB", bytes: 1024 ** 3 },
  { label: "TB", bytes: 1024 ** 4 }
];

function bestByteUnit(bytes: number): { label: string; bytes: number } {
  for (let i = BYTE_UNITS.length - 1; i >= 0; i--) {
    if (bytes >= BYTE_UNITS[i].bytes) return BYTE_UNITS[i];
  }
  return BYTE_UNITS[1]; // default to GB for small/zero values
}

// parseDurationMinutes reads a Go-style duration string (e.g. "720h", "90m",
// "1h30m") and returns the total as minutes. Unrecognized/empty input is 0.
function parseDurationMinutes(value: string): number {
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

const DURATION_UNITS: { label: string; minutes: number }[] = [
  { label: "Minutes", minutes: 1 },
  { label: "Hours", minutes: 60 },
  { label: "Days", minutes: 1440 }
];

function bestDurationUnit(minutes: number): { label: string; minutes: number } {
  for (let i = DURATION_UNITS.length - 1; i >= 0; i--) {
    if (minutes >= DURATION_UNITS[i].minutes) return DURATION_UNITS[i];
  }
  return DURATION_UNITS[0];
}

// DurationField edits one duration env value (stored as total minutes) as a
// friendly "amount + unit" pair, letting TTL be set in minutes, hours, or
// days rather than being locked to whole days.
function DurationField({
  label,
  help,
  minutes,
  disabled,
  onChange
}: {
  label: string;
  help: string;
  minutes: string;
  disabled?: boolean;
  onChange: (minutes: string) => void;
}) {
  const totalMinutes = Number(minutes || "0");
  const [unitLabel, setUnitLabel] = useState(() => bestDurationUnit(totalMinutes).label);
  const unit = DURATION_UNITS.find((u) => u.label === unitLabel) ?? DURATION_UNITS[2];
  // 0 is a real, distinct value here (e.g. the shared Retention age
  // deliberately set to 0 to disable the default sweep) -- unlike
  // ByteSizeField's "0/empty = Unlimited" convention, this must not collapse
  // to a blank input, or there'd be no way to tell "0" from "not typed yet".
  const amount = Number.isFinite(totalMinutes) ? totalMinutes / unit.minutes : NaN;

  function handleAmountChange(raw: string) {
    if (!raw.trim()) {
      onChange("0");
      return;
    }
    const parsed = Number(raw);
    if (Number.isNaN(parsed) || parsed < 0) {
      onChange("0");
      return;
    }
    if (parsed === 0) {
      onChange("0");
      return;
    }
    onChange(String(Math.max(1, Math.round(parsed * unit.minutes))));
  }

  return (
    <label className="duration-field">
      <span>{label}</span>
      <div style={{ display: "flex", gap: 8 }}>
        <input
          type="number"
          min="0"
          step="any"
          value={Number.isNaN(amount) ? "" : amount}
          placeholder={"0"}
          disabled={disabled}
          onChange={(event) => handleAmountChange(event.target.value)}
        />
        <select value={unitLabel} disabled={disabled} onChange={(event) => setUnitLabel(event.target.value)} style={{ maxWidth: 100 }}>
          {DURATION_UNITS.map((u) => (
            <option key={u.label} value={u.label}>{u.label}</option>
          ))}
        </select>
      </div>
      <span className="env-field-desc">{help}</span>
    </label>
  );
}

// ByteSizeField edits one byte-count env value as a friendly
// "amount + unit" pair. bytes is the raw byte count as a string (what the
// backend stores); an empty/zero value displays as "Unlimited".
function ByteSizeField({
  label,
  help,
  bytes,
  onChange
}: {
  label: string;
  help: string;
  bytes: string;
  onChange: (bytes: string) => void;
}) {
  const numericBytes = Number(bytes || "0");
  const [unitLabel, setUnitLabel] = useState(() => bestByteUnit(numericBytes).label);
  const unit = BYTE_UNITS.find((u) => u.label === unitLabel) ?? BYTE_UNITS[1];
  const amount = numericBytes > 0 ? numericBytes / unit.bytes : NaN;

  function handleAmountChange(raw: string) {
    const parsed = Number(raw);
    if (!raw.trim() || Number.isNaN(parsed) || parsed <= 0) {
      onChange("0");
      return;
    }
    onChange(String(Math.round(parsed * unit.bytes)));
  }

  return (
    <label className="duration-field">
      <span>{label}</span>
      <div style={{ display: "flex", gap: 8 }}>
        <input
          type="number"
          min="0"
          step="any"
          value={Number.isNaN(amount) ? "" : amount}
          placeholder={"Unlimited"}
          onChange={(event) => handleAmountChange(event.target.value)}
        />
        <select value={unitLabel} onChange={(event) => setUnitLabel(event.target.value)} style={{ maxWidth: 90 }}>
          {BYTE_UNITS.map((u) => (
            <option key={u.label} value={u.label}>{u.label}</option>
          ))}
        </select>
      </div>
      <span className="env-field-desc">{help}</span>
    </label>
  );
}

// CategoryRetentionModal is a focused editor for the per-category age
// overrides -- pulled out of the main Limits & Retention flow (which was
// getting crowded with 8 extra duration fields) into its own dialog, following
// the same modal-backdrop/command-modal pattern as MintCollectibleUsernameModal.
// It edits the same categoryAgeMinutes state the parent already owns; there's
// no separate save here, just Close -- the one shared "Save limits &
// retention settings" button below still covers these values too.
function CategoryRetentionModal({
  categoryAgeMinutes,
  onChange,
  disabled,
  onClose
}: {
  categoryAgeMinutes: Record<string, string>;
  onChange: (key: string, minutes: string) => void;
  disabled: boolean;
  onClose: () => void;
}) {
  return createPortal(
    <div className="modal-backdrop" role="presentation">
      <section className="modal command-modal" role="dialog" aria-modal="true" aria-label={"Per-category retention overrides"}>
        <div className="modal-head">
          <div>
            <div className="eyebrow">{"Limits & Retention"}</div>
            <h2>{"Per-category overrides"}</h2>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} aria-label={"Close"}><X size={15} /></button>
        </div>
        <div className="command-body">
          <p className="env-field-desc">
            {"Leave a category at 0 to inherit the shared Retention age. The mode switch still applies to all of them -- these only change how old that one category's media must be."}
          </p>
          {CATEGORY_AGE_FIELDS.map((field) => (
            <DurationField
              key={field.key}
              label={field.label}
              help={"Inherits the shared Retention age when left at 0."}
              minutes={categoryAgeMinutes[field.key] || "0"}
              disabled={disabled}
              onChange={(minutes) => onChange(field.key, minutes)}
            />
          ))}
        </div>
        <div className="modal-actions">
          <button className="btn primary" type="button" onClick={onClose}>{"Close"}</button>
        </div>
      </section>
    </div>,
    document.body
  );
}

// ManualPurgeStorageModal lets an operator delete media blob bytes right now
// by hand-picked category (and, optionally, avatars) with an optional
// "created before" age cutoff -- the manual counterpart of the automatic
// hard-retention sweep above. Leaving the date empty purges everything
// matching the selected categories regardless of age. Deletion semantics are
// identical to "hard" retention mode: only the file bytes are removed, never
// the document/photo row, so an affected message still renders its
// placeholder (see internal/app/files.Service.ManualPurge's doc comment).
// Follows the same modal-backdrop/command-modal structure as
// CategoryRetentionModal, but hands the actual mutation off to ActionButton
// (reason + dry-run + confirm) since -- unlike the settings above -- this is
// an immediate, irreversible delete rather than a saved .env value.
function ManualPurgeStorageModal({ onClose }: { onClose: () => void }) {
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
            {CATEGORY_AGE_FIELDS.map((field) => (
              <label key={field.key} className="checkline">
                <input type="checkbox" checked={Boolean(selected[field.key])} onChange={() => toggle(field.key)} />
                {" "}{field.label}
              </label>
            ))}
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

function LimitsRetentionSection() {
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState("");
  const [initial, setInitial] = useState<Record<string, string>>({});
  const [blobBackend, setBlobBackend] = useState("s3");
  const [maxTotalBytes, setMaxTotalBytes] = useState("0");
  const [minFreeBytes, setMinFreeBytes] = useState("0");
  const [maxUploadFileBytes, setMaxUploadFileBytes] = useState("0");
  const [retentionMode, setRetentionMode] = useState("off");
  const [retentionAgeMinutes, setRetentionAgeMinutes] = useState("43200");
  const [categoryAgeMinutes, setCategoryAgeMinutes] = useState<Record<string, string>>({});
  const [categoryModalOpen, setCategoryModalOpen] = useState(false);
  const [evictionEnable, setEvictionEnable] = useState(false);
  const [manualPurgeOpen, setManualPurgeOpen] = useState(false);

  async function load() {
    setError("");
    try {
      const groups = await api.serverEnv();
      const values: Record<string, string> = {};
      for (const group of groups) {
        for (const field of group.fields) {
          values[field.key] = field.value || field.default_value || "";
        }
      }
      setInitial(values);
      setBlobBackend((values[STORAGE_ENV_KEYS.blobBackend] || "s3").trim().toLowerCase());
      setMaxTotalBytes(values[STORAGE_ENV_KEYS.maxTotal] || "0");
      setMinFreeBytes(values[STORAGE_ENV_KEYS.minFree] || "0");
      setMaxUploadFileBytes(values[STORAGE_ENV_KEYS.maxUploadFile] || "0");
      const mode = (values[STORAGE_ENV_KEYS.retentionMode] || "off").trim().toLowerCase();
      setRetentionMode(mode === "orphan" || mode === "hard" ? mode : "off");
      const mins = parseDurationMinutes(values[STORAGE_ENV_KEYS.retentionMaxAge] || "720h");
      setRetentionAgeMinutes(mins > 0 ? String(Math.max(1, Math.round(mins))) : "43200");
      const nextCategoryAges: Record<string, string> = {};
      for (const field of CATEGORY_AGE_FIELDS) {
        const raw = values[field.envKey] || "";
        const categoryMins = parseDurationMinutes(raw);
        nextCategoryAges[field.key] = categoryMins > 0 ? String(Math.round(categoryMins)) : "0";
      }
      setCategoryAgeMinutes(nextCategoryAges);
      setEvictionEnable((values[STORAGE_ENV_KEYS.evictionEnable] || "false").trim().toLowerCase() === "true");
      setLoaded(true);
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  useEffect(() => { void load(); }, []);

  // Only the age is re-encoded from its friendly "days" input back to a Go
  // duration string; mode/off keeps whatever TELESRV_STORAGE_RETENTION_MAX_AGE
  // already was rather than clobbering it with a throwaway placeholder.
  const pendingValues = useMemo(() => {
    const next: Record<string, string> = {
      [STORAGE_ENV_KEYS.maxTotal]: maxTotalBytes || "0",
      [STORAGE_ENV_KEYS.minFree]: minFreeBytes || "0",
      [STORAGE_ENV_KEYS.maxUploadFile]: maxUploadFileBytes || "0",
      [STORAGE_ENV_KEYS.retentionMode]: retentionMode,
      [STORAGE_ENV_KEYS.retentionMaxAge]: retentionMode === "off"
        ? (initial[STORAGE_ENV_KEYS.retentionMaxAge] || "720h")
        // 0 is a legitimate value here: "no sweep by default", left for
        // per-category overrides below to opt specific categories in. Don't
        // clamp it up to a minimum of 1 -- that used to make it impossible to
        // ever save a 0 global age.
        : `${Math.max(0, Math.round(Number(retentionAgeMinutes || "0")))}m`,
      [STORAGE_ENV_KEYS.evictionEnable]: evictionEnable ? "true" : "false"
    };
    // Per-category overrides: a field left at "inherit global" (0) saves as
    // empty rather than a redundant explicit duration, matching the "unset
    // means inherit" contract on the server side.
    for (const field of CATEGORY_AGE_FIELDS) {
      const mins = Math.round(Number(categoryAgeMinutes[field.key] || "0"));
      next[field.envKey] = mins > 0 ? `${mins}m` : "";
    }
    const changed: Record<string, string> = {};
    for (const [key, value] of Object.entries(next)) {
      if ((initial[key] ?? "") !== value) changed[key] = value;
    }
    return changed;
  }, [maxTotalBytes, minFreeBytes, maxUploadFileBytes, retentionMode, retentionAgeMinutes, categoryAgeMinutes, evictionEnable, initial]);

  const hasChanges = Object.keys(pendingValues).length > 0;
  const uploadCeilingExceeded = Number(maxUploadFileBytes || "0") > PROTOCOL_UPLOAD_CEILING_BYTES;

  return (
    <section className="section-block">
      <SectionHead
        title={"Limits & Retention"}
        text={"Server-wide storage budget, per-file upload cap, and automatic media cleanup. Saved to .env -- takes effect on the next Restart/Update."}
      />
      {error && <Alert>{error}</Alert>}
      {!loaded ? (
        <p style={{ color: "var(--muted)" }}>{"Loading current settings..."}</p>
      ) : (
        <div className="card-body">
          <div className="attr-block">
            <ByteSizeField
              label={"Max total storage budget"}
              help={"Reject new uploads once total tracked blob bytes would exceed this. Empty/0 = unlimited."}
              bytes={maxTotalBytes}
              onChange={setMaxTotalBytes}
            />
            {blobBackend === "localfs" && (
              <ByteSizeField
                label={"Min free space guard"}
                help={"localfs backend only: reject new uploads once real free disk space falls below this. Empty/0 disables the check."}
                bytes={minFreeBytes}
                onChange={setMinFreeBytes}
              />
            )}
            <ByteSizeField
              label={"Max single file size"}
              help={"Reject a single upload once its total assembled size exceeds this. Empty/0 = unlimited, bounded only by the protocol's own ~4GB per-file ceiling."}
              bytes={maxUploadFileBytes}
              onChange={setMaxUploadFileBytes}
            />
            {uploadCeilingExceeded && (
              <Alert>{"This exceeds the protocol's own ~4GB upload ceiling and will be refused when the server restarts."}</Alert>
            )}
          </div>

          <div className="attr-block" style={{ marginTop: "1.5em" }}>
            <label className="duration-field">
              <span>{"Retention mode"}</span>
              <select value={retentionMode} onChange={(event) => setRetentionMode(event.target.value)}>
                <option value="off">{"Off"}</option>
                <option value="orphan">{"Delete once no longer used (safe)"}</option>
                <option value="hard">{"Delete after a fixed time, even if still in use"}</option>
              </select>
            </label>
            <p className="env-field-desc">
              {retentionMode === "off" &&
                "No storage sweep runs; nothing is auto-deleted. Storage usage is still tracked and shown above either way."}
              {retentionMode === "orphan" &&
                "Safe: a document or photo's file is deleted only once it is no longer referenced by any message, profile photo, or sticker set. Media still visible in a conversation is never touched, regardless of age."}
              {retentionMode === "hard" &&
                "Irreversible and aggressive: a document or photo's file bytes are deleted once old enough, REGARDLESS of whether a message still references it. Old media in active conversations will start showing as unavailable once purged -- only the file is removed, the message itself keeps rendering its placeholder (name, size, thumbnail)."}
            </p>
            <DurationField
              label={"Retention age"}
              help={
                (retentionMode === "hard"
                  ? "How old the media itself must be, counted from when it was uploaded, before its bytes are purged."
                  : "How long a document or photo must have had zero references before its file is deleted.") +
                " Set to 0 to disable the sweep by default and only clean up categories you explicitly override below."
              }
              minutes={retentionAgeMinutes}
              disabled={retentionMode === "off"}
              onChange={setRetentionAgeMinutes}
            />
          </div>

          <div className="attr-block" style={{ marginTop: "1.5em" }}>
            <button className="btn icon-text" type="button" onClick={() => setCategoryModalOpen(true)}>
              <Settings2 size={15} /> {"Per-category overrides..."}
            </button>
            <span className="env-field-desc">
              {"Optional -- give Photo/Video/GIF/Music/Voice/File/Avatar their own retention age instead of the shared one above."}
            </span>
          </div>
          {categoryModalOpen && (
            <CategoryRetentionModal
              categoryAgeMinutes={categoryAgeMinutes}
              onChange={(key, minutes) => setCategoryAgeMinutes((prev) => ({ ...prev, [key]: minutes }))}
              disabled={retentionMode === "off"}
              onClose={() => setCategoryModalOpen(false)}
            />
          )}

          <div className="attr-block" style={{ marginTop: "1.5em" }}>
            <label className="checkline">
              <input
                type="checkbox"
                checked={evictionEnable}
                onChange={(event) => setEvictionEnable(event.target.checked)}
              />
              {" "}{"Actively reclaim space once over budget"}
            </label>
            <p className="env-field-desc">
              {"Once total physical storage exceeds the Max total storage budget above, actively delete the oldest files (regardless of category or age) until back under budget -- the same way \"hard\" retention mode purges files. Independent of the retention mode above: this can run even when that's Off. Off by default, since this changes the storage budget from block-new-uploads-only to also reclaiming from existing files."}
            </p>
          </div>

          <div className="attr-block" style={{ marginTop: "1.5em" }}>
            <button className="btn danger icon-text" type="button" onClick={() => setManualPurgeOpen(true)}>
              <Settings2 size={15} /> {"Manually purge storage..."}
            </button>
            <span className="env-field-desc">
              {"Delete media file bytes right now by hand-picked category and an optional age cutoff, independent of the retention settings above."}
            </span>
          </div>
          {manualPurgeOpen && <ManualPurgeStorageModal onClose={() => setManualPurgeOpen(false)} />}

          <div className="gift-table-actions env-save-row">
            <ActionButton
              tone="warn"
              label={"Save limits & retention settings"}
              path="/api/actions/update-server-env"
              payload={() => ({ values: pendingValues })}
              disabled={!hasChanges || uploadCeilingExceeded}
              onDone={() => void load()}
            />
          </div>
        </div>
      )}
    </section>
  );
}
