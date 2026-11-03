import { CloudCheck, HardDrive, TrashBin } from "@gravity-ui/icons";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { api } from "../api";
import { formatBytes } from "../lib/format";
import {
  CATEGORY_AGE_FIELDS,
  ManualPurgeStorageModal,
  PROTOCOL_UPLOAD_CEILING_BYTES,
  parseDurationMinutes
} from "../pages/StoragePage";
import type { EnvGroup, StorageStatsResponse } from "../types";
import { ActionButton } from "./ActionButton";
import { Alert } from "./ui";

// StorageSettings is the "Storage & Media" .env group drawn as what it is --
// where files live, how much may be stored, and when old media is cleaned up --
// instead of 25 raw keys. It edits the same keys and saves only the ones that
// changed, through the same update-server-env action as every other group.

const K = {
  backend: "TELESRV_BLOB_BACKEND",
  blobDir: "TELESRV_BLOB_DIR",
  s3Endpoint: "TELESRV_S3_ENDPOINT",
  s3Region: "TELESRV_S3_REGION",
  s3Bucket: "TELESRV_S3_BUCKET",
  s3Key: "TELESRV_S3_ACCESS_KEY_ID",
  s3Secret: "TELESRV_S3_SECRET_ACCESS_KEY",
  s3CreateBucket: "TELESRV_S3_CREATE_BUCKET",
  s3SSL: "TELESRV_S3_USE_SSL",
  s3PathStyle: "TELESRV_S3_PATH_STYLE",
  guard: "TELESRV_STORAGE_LOW_SPACE_GUARD_ENABLE",
  minFree: "TELESRV_STORAGE_MIN_FREE_BYTES",
  maxTotal: "TELESRV_STORAGE_MAX_TOTAL_BYTES",
  maxFile: "TELESRV_STORAGE_MAX_UPLOAD_FILE_BYTES",
  retentionMode: "TELESRV_STORAGE_RETENTION_MODE",
  retentionAge: "TELESRV_STORAGE_RETENTION_MAX_AGE",
  eviction: "TELESRV_STORAGE_EVICTION_ENABLE"
} as const;

// The keys this panel draws itself; anything else in the group (refresh
// intervals and the like) is listed under "Other" as plain fields. Built on
// first use: StoragePage imports this module too, so its exports may not be
// initialised yet while this module loads.
let drawnKeys: Set<string> | null = null;
function drawn(): Set<string> {
  drawnKeys ??= new Set<string>([...Object.values(K), ...CATEGORY_AGE_FIELDS.map((f) => f.envKey)]);
  return drawnKeys;
}

/** Whether a .env group is the one StorageSettings draws. */
export function isStorageGroup(group: EnvGroup): boolean {
  return group.fields.some((f) => f.key === K.maxTotal);
}

const SIZE_UNITS = [
  { label: "MB", bytes: 1024 ** 2 },
  { label: "GB", bytes: 1024 ** 3 },
  { label: "TB", bytes: 1024 ** 4 }
];
const AGE_UNITS = [
  { label: "hours", minutes: 60 },
  { label: "days", minutes: 1440 }
];

function pickSizeUnit(bytes: number) {
  for (let i = SIZE_UNITS.length - 1; i >= 0; i--) if (bytes >= SIZE_UNITS[i].bytes) return SIZE_UNITS[i];
  return SIZE_UNITS[1];
}

function pickAgeUnit(minutes: number) {
  return minutes > 0 && minutes % 1440 !== 0 && minutes < 1440 * 3 ? AGE_UNITS[0] : AGE_UNITS[1];
}

// SizeInput edits a byte count as "amount + unit"; empty or 0 means "no limit".
function SizeInput({ bytes, onChange, label, max }: { bytes: string; onChange: (v: string) => void; label: string; max?: number }) {
  const n = Number(bytes || "0");
  const [unit, setUnit] = useState(() => pickSizeUnit(n));
  const amount = n > 0 ? +(n / unit.bytes).toFixed(2) : "";
  return (
    <span className="unit-input">
      <input
        type="number"
        min="0"
        step="any"
        value={amount}
        placeholder={"No limit"}
        aria-label={label}
        onChange={(event) => {
          const v = Number(event.target.value);
          let out = !event.target.value || !(v > 0) ? 0 : Math.round(v * unit.bytes);
          if (max && out > max) out = max;
          onChange(String(out));
        }}
      />
      <select value={unit.label} onChange={(event) => setUnit(SIZE_UNITS.find((u) => u.label === event.target.value) ?? SIZE_UNITS[1])} aria-label={`${label} unit`}>
        {SIZE_UNITS.map((u) => <option key={u.label}>{u.label}</option>)}
      </select>
    </span>
  );
}

// AgeInput edits a Go duration ("720h", "90m") as "amount + hours/days".
// An empty value means "inherit" where inheritOK is set, else 0.
function AgeInput({ value, onChange, label, inheritOK }: { value: string; onChange: (v: string) => void; label: string; inheritOK?: boolean }) {
  const minutes = Math.round(parseDurationMinutes(value || ""));
  const [unit, setUnit] = useState(() => pickAgeUnit(minutes));
  const amount = minutes > 0 ? +(minutes / unit.minutes).toFixed(2) : value === "" && inheritOK ? "" : 0;
  return (
    <span className="unit-input">
      <input
        type="number"
        min="0"
        step="any"
        value={amount}
        placeholder={inheritOK ? "Default" : "0"}
        aria-label={label}
        onChange={(event) => {
          if (!event.target.value) return onChange(inheritOK ? "" : "0m");
          const v = Number(event.target.value);
          onChange(v > 0 ? `${Math.round(v * unit.minutes)}m` : "0m");
        }}
      />
      <select value={unit.label} onChange={(event) => setUnit(AGE_UNITS.find((u) => u.label === event.target.value) ?? AGE_UNITS[1])} aria-label={`${label} unit`}>
        {AGE_UNITS.map((u) => <option key={u.label}>{u.label}</option>)}
      </select>
    </span>
  );
}

function Toggle({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label: string }) {
  return (
    <label className="switch">
      <input type="checkbox" checked={checked} onChange={(event) => onChange(event.target.checked)} aria-label={label} />
      <span className="switch-track" aria-hidden="true" />
    </label>
  );
}

function Row({ title, hint, children }: { title: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <div className="settings-field">
      <div className="settings-field-text">
        <span className="storage-row-title">{title}</span>
        {hint && <span className="settings-field-desc">{hint}</span>}
      </div>
      <div className="settings-field-control">{children}</div>
    </div>
  );
}

function Choice({ active, icon, title, text, onClick }: { active: boolean; icon?: ReactNode; title: string; text: string; onClick: () => void }) {
  return (
    <button type="button" className={`choice-card ${active ? "active" : ""}`.trim()} onClick={onClick} aria-pressed={active}>
      {icon && <span className="choice-card-icon">{icon}</span>}
      <span className="choice-card-text">
        <strong>{title}</strong>
        <span>{text}</span>
      </span>
      <span className="choice-card-radio" aria-hidden="true" />
    </button>
  );
}

const noAutofill = {
  autoComplete: "off",
  spellCheck: false,
  "data-1p-ignore": "",
  "data-lpignore": "true",
  "data-bwignore": "true"
} as const;

export function StorageSettings({ group, onSaved }: { group: EnvGroup; onSaved: () => void }) {
  // What .env holds; a key that is not written yet shows its default.
  const initial = useMemo(
    () => Object.fromEntries(group.fields.map((f) => [f.key, f.value || f.default_value || ""])),
    [group]
  );
  const [values, setValues] = useState<Record<string, string>>(initial);
  useEffect(() => setValues(initial), [initial]);
  const [stats, setStats] = useState<StorageStatsResponse | null>(null);
  const [purgeOpen, setPurgeOpen] = useState(false);

  useEffect(() => {
    api.storageStats().then(setStats).catch(() => undefined);
  }, []);

  const get = (key: string) => values[key] ?? "";
  const set = (key: string) => (v: string) => setValues((prev) => ({ ...prev, [key]: v }));
  const on = (key: string) => get(key).trim().toLowerCase() === "true";
  const flip = (key: string) => (v: boolean) => set(key)(v ? "true" : "false");

  const backend = (get(K.backend) || (get(K.s3Endpoint) ? "s3" : "localfs")).toLowerCase();
  const mode = (get(K.retentionMode) || "off").toLowerCase();

  const changed = useMemo(() => {
    const out: Record<string, string> = {};
    for (const f of group.fields) {
      const now = values[f.key] ?? "";
      if (now !== (initial[f.key] ?? "")) out[f.key] = now;
    }
    return out;
  }, [values, initial, group]);
  const changedCount = Object.keys(changed).length;

  const used = Number(stats?.PhysicalBytes ?? 0);
  const budget = Number(get(K.maxTotal) || "0");
  const usedPct = budget > 0 ? Math.min(100, (used / budget) * 100) : 0;
  const fileTooBig = Number(get(K.maxFile) || "0") > PROTOCOL_UPLOAD_CEILING_BYTES;
  const others = group.fields.filter((f) => !drawn().has(f.key));

  return (
    <div className="storage-settings">
      <section className="section-block settings-panel">
        <h3 className="storage-heading">{"Where files are kept"}</h3>
        <div className="choice-grid">
          <Choice
            active={backend !== "s3"}
            icon={<HardDrive />}
            title={"This server's disk"}
            text={"Simple; a folder next to the server."}
            onClick={() => set(K.backend)("localfs")}
          />
          <Choice
            active={backend === "s3"}
            icon={<CloudCheck />}
            title={"S3-compatible storage"}
            text={"AWS S3, MinIO, Cloudflare R2 and the like."}
            onClick={() => set(K.backend)("s3")}
          />
        </div>
        {backend === "s3" ? (
          <div className="settings-fields">
            <Row title={"Endpoint"} hint={"Host or host:port, without http://"}>
              <input value={get(K.s3Endpoint)} onChange={(e) => set(K.s3Endpoint)(e.target.value)} placeholder={"s3.amazonaws.com"} aria-label={"Endpoint"} name="s3-endpoint" {...noAutofill} />
            </Row>
            <Row title={"Bucket and region"}>
              <span className="unit-input">
                <input value={get(K.s3Bucket)} onChange={(e) => set(K.s3Bucket)(e.target.value)} placeholder={"bucket"} aria-label={"Bucket"} name="s3-bucket" {...noAutofill} />
                <input value={get(K.s3Region)} onChange={(e) => set(K.s3Region)(e.target.value)} placeholder={"us-east-1"} aria-label={"Region"} name="s3-region" style={{ maxWidth: 120 }} {...noAutofill} />
              </span>
            </Row>
            <Row title={"Access key"}>
              <input value={get(K.s3Key)} onChange={(e) => set(K.s3Key)(e.target.value)} placeholder={"Access key ID"} aria-label={"Access key ID"} name="s3-access-key" {...noAutofill} />
            </Row>
            <Row title={"Secret key"}>
              <input type="password" value={get(K.s3Secret)} onChange={(e) => set(K.s3Secret)(e.target.value)} placeholder={"Secret access key"} aria-label={"Secret access key"} name="s3-secret-key" {...noAutofill} autoComplete="new-password" />
            </Row>
            <Row title={"Use HTTPS"}><Toggle checked={on(K.s3SSL)} onChange={flip(K.s3SSL)} label={"Use HTTPS"} /></Row>
            <Row title={"Path-style URLs"} hint={"On for MinIO and most self-hosted S3."}><Toggle checked={on(K.s3PathStyle)} onChange={flip(K.s3PathStyle)} label={"Path-style URLs"} /></Row>
            <Row title={"Create the bucket if missing"}><Toggle checked={on(K.s3CreateBucket)} onChange={flip(K.s3CreateBucket)} label={"Create the bucket"} /></Row>
          </div>
        ) : (
          <div className="settings-fields">
            <Row title={"Folder"} hint={"Relative paths start at the server folder."}>
              <input value={get(K.blobDir)} onChange={(e) => set(K.blobDir)(e.target.value)} placeholder={"data/blobs"} aria-label={"Folder"} name="blob-dir" {...noAutofill} />
            </Row>
          </div>
        )}
      </section>

      <section className="section-block settings-panel">
        <h3 className="storage-heading">{"Space"}</h3>
        <div className="capacity">
          <div className="capacity-numbers">
            <strong>{stats ? formatBytes(String(used)) : "…"}</strong>
            <span>{budget > 0 ? `of ${formatBytes(String(budget))} budget` : "used, no budget set"}</span>
          </div>
          <div className={`capacity-bar ${usedPct >= 90 ? "danger" : usedPct >= 75 ? "warn" : ""}`.trim()}>
            <span style={{ width: `${budget > 0 ? Math.max(usedPct, used > 0 ? 1 : 0) : 0}%` }} />
          </div>
        </div>
        <div className="settings-fields">
          <Row title={"Storage budget"} hint={"New uploads are refused once stored files would go past this."}>
            <SizeInput bytes={get(K.maxTotal)} onChange={set(K.maxTotal)} label={"Storage budget"} />
          </Row>
          <Row title={"Free space when over budget"} hint={"Delete the oldest files until back under the budget, instead of only refusing uploads."}>
            <Toggle checked={on(K.eviction)} onChange={flip(K.eviction)} label={"Free space when over budget"} />
          </Row>
          <Row title={"Largest single file"} hint={"The protocol itself stops at about 4 GB."}>
            <SizeInput bytes={get(K.maxFile)} onChange={set(K.maxFile)} label={"Largest single file"} max={PROTOCOL_UPLOAD_CEILING_BYTES} />
          </Row>
          <Row title={"Low disk space guard"} hint={"Refuse uploads when the server's own disk is nearly full."}>
            <Toggle checked={on(K.guard)} onChange={flip(K.guard)} label={"Low disk space guard"} />
          </Row>
          {on(K.guard) && (
            <Row title={"Keep at least this much free"}>
              <SizeInput bytes={get(K.minFree)} onChange={set(K.minFree)} label={"Minimum free space"} />
            </Row>
          )}
        </div>
        {fileTooBig && <Alert>{"That is over the protocol's ~4 GB file limit; the server would refuse it on restart."}</Alert>}
      </section>

      <section className="section-block settings-panel">
        <h3 className="storage-heading">{"Cleaning up old media"}</h3>
        <div className="choice-grid three">
          <Choice active={mode === "off"} title={"Keep everything"} text={"Nothing is deleted automatically."} onClick={() => set(K.retentionMode)("off")} />
          <Choice active={mode === "orphan"} title={"Unused files only"} text={"Delete files no message, avatar or pack uses any more."} onClick={() => set(K.retentionMode)("orphan")} />
          <Choice active={mode === "hard"} title={"Anything older than"} text={"Delete old files even from chats; messages show a placeholder."} onClick={() => set(K.retentionMode)("hard")} />
        </div>
        {mode !== "off" && (
          <>
            <div className="settings-fields">
              <Row title={mode === "hard" ? "Delete files older than" : "Delete once unused for"}>
                <AgeInput value={get(K.retentionAge)} onChange={set(K.retentionAge)} label={"Retention age"} />
              </Row>
            </div>
            <div className="age-grid-head">{"Per type (empty = the age above)"}</div>
            <div className="age-grid">
              {CATEGORY_AGE_FIELDS.map((field) => (
                <label key={field.key} className="age-cell">
                  <span>{field.label}</span>
                  <AgeInput value={get(field.envKey)} onChange={set(field.envKey)} label={field.label} inheritOK />
                </label>
              ))}
            </div>
          </>
        )}
        <div className="storage-purge">
          <span>{"Need space right now? Delete chosen types of media by hand."}</span>
          <button className="btn danger icon-text" type="button" onClick={() => setPurgeOpen(true)}>
            <TrashBin /> {"Purge media…"}
          </button>
        </div>
      </section>

      {others.length > 0 && (
        <section className="section-block settings-panel">
          <h3 className="storage-heading">{"Other"}</h3>
          <div className="settings-fields">
            {others.map((f) => (
              <Row key={f.key} title={f.key} hint={f.description}>
                <input value={get(f.key)} onChange={(e) => set(f.key)(e.target.value)} placeholder={f.default_value} aria-label={f.key} name={`env-${f.key}`} {...noAutofill} />
              </Row>
            ))}
          </div>
        </section>
      )}

      <div className="settings-save-bar">
        <span className="settings-save-note">
          {changedCount > 0 ? `${changedCount} unsaved change${changedCount === 1 ? "" : "s"} · applies on the next restart` : "All saved."}
        </span>
        {changedCount > 0 && <button className="btn" type="button" onClick={() => setValues(initial)}>{"Discard"}</button>}
        <ActionButton
          tone="warn"
          label={changedCount > 0 ? `Save ${changedCount} change${changedCount === 1 ? "" : "s"}` : "Save"}
          path="/api/actions/update-server-env"
          payload={() => ({ values: changed })}
          disabled={changedCount === 0 || fileTooBig}
          onDone={onSaved}
        />
      </div>

      {purgeOpen && <ManualPurgeStorageModal onClose={() => setPurgeOpen(false)} />}
    </div>
  );
}
