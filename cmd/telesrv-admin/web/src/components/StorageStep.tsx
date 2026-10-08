import { ArrowRight, Loader2 } from "lucide-react";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import { RestartOverlay, useAdminRestartWatcher } from "../pages/ServerSettingsPage";
import type { StorageSettings } from "../types";
import { Alert } from "./ui";

const WIZARD_REASON = "Set from the first-run setup wizard";

const DEFAULT_STORAGE: StorageSettings = {
  postgres_mode: "embedded",
  postgres_dsn: "",
  embedded_dir: "data/postgres",
  blob_backend: "localfs",
  blob_dir: "data/blobs",
  s3_endpoint: "",
  s3_bucket: "owpengram-media",
  s3_region: "us-east-1",
  s3_access_key_id: "",
  s3_secret_access_key: "",
  s3_secret_set: false,
  s3_use_ssl: false,
  s3_path_style: true,
  s3_create_bucket: false,
  configured: false
};

// Where the server keeps its data. Nothing runs until this is saved: the
// supervisor (telesrv-ctl) starts PostgreSQL and the server once .env names
// them, so this is also what brings the server up for the first time. The
// panel restarts itself on save and the page reloads into the next step.
export function StorageStep() {
  const [form, setForm] = useState<StorageSettings>(DEFAULT_STORAGE);
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const restartWatcher = useAdminRestartWatcher();

  useEffect(() => {
    let cancelled = false;
    api.serverStorage()
      .then((current) => {
        if (cancelled) return;
        setForm({ ...DEFAULT_STORAGE, ...current, s3_secret_access_key: "" });
        setLoaded(true);
      })
      .catch((err) => {
        if (cancelled) return;
        setError(errorMessage(err));
        setLoaded(true);
      });
    return () => { cancelled = true; };
  }, []);

  function set<K extends keyof StorageSettings>(key: K, value: StorageSettings[K]) {
    setForm((prev) => ({ ...prev, [key]: value }));
  }

  async function submit() {
    setBusy(true);
    setError("");
    try {
      const result = await api.action("/api/actions/configure-storage", {
        command_id: "", reason: WIZARD_REASON, confirm: true,
        postgres_mode: form.postgres_mode,
        postgres_dsn: form.postgres_dsn,
        embedded_dir: form.embedded_dir,
        blob_backend: form.blob_backend,
        blob_dir: form.blob_dir,
        s3_endpoint: form.s3_endpoint,
        s3_bucket: form.s3_bucket,
        s3_region: form.s3_region,
        s3_access_key_id: form.s3_access_key_id,
        s3_secret_access_key: form.s3_secret_access_key ?? "",
        s3_use_ssl: form.s3_use_ssl,
        s3_path_style: form.s3_path_style,
        s3_create_bucket: form.s3_create_bucket
      });
      if (result.error) {
        setError(result.error);
        setBusy(false);
        return;
      }
      void restartWatcher.watch(900000);
    } catch (err) {
      setError(errorMessage(err));
      setBusy(false);
    }
  }

  const external = form.postgres_mode === "external";
  const s3 = form.blob_backend === "s3";

  return (
    <div className="wizard-step-body">
      <p className="wizard-step-hint">{"Choose where the server keeps its data. It is checked before anything is saved."}</p>
      {error && <Alert>{error}</Alert>}

      <strong>{"Database"}</strong>
      <label className="checkline">
        <input type="radio" name="pgmode" checked={!external} disabled={!loaded} onChange={() => set("postgres_mode", "embedded")} />
        {" Built-in PostgreSQL (recommended) -- nothing to install, it runs with the server"}
      </label>
      {!external && (
        <label className="form-field env-field">
          <span>{"Data folder"}</span>
          <input value={form.embedded_dir} spellCheck={false} onChange={(event) => set("embedded_dir", event.target.value)} />
        </label>
      )}
      <label className="checkline">
        <input type="radio" name="pgmode" checked={external} disabled={!loaded} onChange={() => set("postgres_mode", "external")} />
        {" My own PostgreSQL"}
      </label>
      {external && (
        <label className="form-field env-field">
          <span>{"Connection string"}</span>
          <span className="env-field-desc">{"The database must already exist. It can be empty or already belong to this server."}</span>
          <input
            value={form.postgres_dsn}
            spellCheck={false}
            autoCapitalize="none"
            placeholder={"postgres://user:password@127.0.0.1:5432/owpengram?sslmode=disable"}
            onChange={(event) => set("postgres_dsn", event.target.value)}
          />
        </label>
      )}

      <strong>{"Media (photos, files, stickers)"}</strong>
      <label className="checkline">
        <input type="radio" name="blob" checked={!s3} disabled={!loaded} onChange={() => set("blob_backend", "localfs")} />
        {" Local disk"}
      </label>
      {!s3 && (
        <label className="form-field env-field">
          <span>{"Folder"}</span>
          <input value={form.blob_dir} spellCheck={false} onChange={(event) => set("blob_dir", event.target.value)} />
        </label>
      )}
      <label className="checkline">
        <input type="radio" name="blob" checked={s3} disabled={!loaded} onChange={() => set("blob_backend", "s3")} />
        {" S3-compatible storage (AWS S3, MinIO, ...)"}
      </label>
      {s3 && (
        <>
          <label className="form-field env-field">
            <span>{"Endpoint"}</span>
            <span className="env-field-desc">{"Host or host:port, without http://. For example s3.amazonaws.com or 127.0.0.1:9000."}</span>
            <input value={form.s3_endpoint} spellCheck={false} autoCapitalize="none" onChange={(event) => set("s3_endpoint", event.target.value)} />
          </label>
          <label className="form-field env-field">
            <span>{"Bucket"}</span>
            <input value={form.s3_bucket} spellCheck={false} autoCapitalize="none" onChange={(event) => set("s3_bucket", event.target.value)} />
          </label>
          <label className="form-field env-field">
            <span>{"Region"}</span>
            <input value={form.s3_region} spellCheck={false} autoCapitalize="none" onChange={(event) => set("s3_region", event.target.value)} />
          </label>
          <label className="form-field env-field">
            <span>{"Access key"}</span>
            <input value={form.s3_access_key_id} spellCheck={false} autoCapitalize="none" onChange={(event) => set("s3_access_key_id", event.target.value)} />
          </label>
          <label className="form-field env-field">
            <span>{"Secret key"}</span>
            <input
              type="password"
              autoComplete="new-password"
              value={form.s3_secret_access_key ?? ""}
              placeholder={form.s3_secret_set ? "(unchanged)" : ""}
              onChange={(event) => set("s3_secret_access_key", event.target.value)}
            />
          </label>
          <label className="checkline">
            <input type="checkbox" checked={form.s3_use_ssl} onChange={(event) => set("s3_use_ssl", event.target.checked)} />
            {" Use HTTPS"}
          </label>
          <label className="checkline">
            <input type="checkbox" checked={form.s3_path_style} onChange={(event) => set("s3_path_style", event.target.checked)} />
            {" Path-style URLs (needed by MinIO, not by AWS S3)"}
          </label>
          <label className="checkline">
            <input type="checkbox" checked={form.s3_create_bucket} onChange={(event) => set("s3_create_bucket", event.target.checked)} />
            {" Create the bucket if it does not exist"}
          </label>
        </>
      )}

      <div className="wizard-actions">
        <button className="btn primary icon-text" type="button" disabled={busy || !loaded} onClick={() => void submit()}>
          {busy ? <Loader2 className="spin" size={15} /> : <ArrowRight size={15} />}
          {"Save and start the server"}
        </button>
      </div>
      {restartWatcher.waiting && (
        <RestartOverlay timedOut={false} onDismiss={restartWatcher.dismiss} logLines={restartWatcher.logLines} />
      )}
      {restartWatcher.timedOut && (
        <RestartOverlay timedOut={true} onDismiss={restartWatcher.dismiss} logLines={restartWatcher.logLines} />
      )}
    </div>
  );
}
