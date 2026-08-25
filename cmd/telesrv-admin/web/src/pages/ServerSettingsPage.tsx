import { ChevronDown, ImageOff, ImagePlus, Loader2, RefreshCw, Trash2, Upload, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { Alert, LoadingSurface, Metric, PageFrame, SectionHead } from "../components/ui";
import type { EnvGroup, ServerIdentity, ServerStatus } from "../types";

// Server Settings: the web-panel equivalent of tui-panel/server-panel.py's
// menu -- admin-editable server name/description/icon (served to clients
// over /owpengram/server-info + /owpengram/server-icon), .env editing, and
// Restart/Update. See cmd/telesrv-admin/serversettings.go for the backend.
export function ServerSettingsPage() {
  return (
    <PageFrame title={"Server Settings"} eyebrow={"Identity, .env, and process control -- mirrors the TUI panel"}>
      <div className="stacked-sections">
        <IdentitySection />
        <ServerControlSection />
        <EnvSection />
      </div>
    </PageFrame>
  );
}

// --- Identity ---------------------------------------------------------

function IdentitySection() {
  const [identity, setIdentity] = useState<ServerIdentity | null>(null);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [iconModalOpen, setIconModalOpen] = useState(false);
  const [iconBust, setIconBust] = useState(0);
  const [iconFailed, setIconFailed] = useState(false);
  const [error, setError] = useState("");

  async function load() {
    setError("");
    try {
      const info = await api.serverIdentity();
      setIdentity(info);
      setName(info.name);
      setDescription(info.description);
      setIconFailed(false);
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  useEffect(() => { void load(); }, []);

  return (
    <section className="section-block">
      <SectionHead title={"Server identity"} text={"Shown to clients auto-fetching this server's key -- takes effect immediately, no restart needed."} />
      {error && <Alert>{error}</Alert>}
      {!identity ? (
        <LoadingSurface label={"Loading identity..."} />
      ) : (
        <div className="card-body">
          <div className="entity-head-main">
            <div className="avatar-edit-slot">
              {identity.icon_ext && !iconFailed ? (
                <img
                  className="avatar-photo-img"
                  src={api.serverIconURL() + `&b=${iconBust}`}
                  alt=""
                  style={{ width: 56, height: 56 }}
                  onError={() => setIconFailed(true)}
                />
              ) : (
                <div className="avatar-fallback server-icon-fallback" style={{ width: 56, height: 56 }}>
                  <ImageOff size={20} />
                </div>
              )}
              <button
                className="icon-btn avatar-edit-btn"
                type="button"
                aria-label={"Change server icon"}
                title={"Change server icon"}
                onClick={() => setIconModalOpen(true)}
              >
                <ImagePlus size={13} />
              </button>
            </div>
            <div className="server-identity-fields">
              <label className="form-field"><span>{"Name"}</span><input value={name} maxLength={128} onChange={(event) => setName(event.target.value)} /></label>
              <label className="form-field"><span>{"Description"}</span><input value={description} maxLength={512} onChange={(event) => setDescription(event.target.value)} /></label>
            </div>
          </div>
          <div className="gift-table-actions">
            <ActionButton
              tone="neutral"
              label={"Save identity"}
              path="/api/actions/set-server-identity"
              payload={() => ({ name, description })}
              onDone={() => void load()}
            />
          </div>
        </div>
      )}
      {iconModalOpen && (
        <ServerIconModal
          hasIcon={!!identity?.icon_ext}
          onClose={() => setIconModalOpen(false)}
          onDone={() => { setIconBust((n) => n + 1); setIconFailed(false); void load(); }}
        />
      )}
    </section>
  );
}

function ServerIconModal({ hasIcon, onClose, onDone }: { hasIcon: boolean; onClose: () => void; onDone: () => void }) {
  const [file, setFile] = useState<File | null>(null);
  const [previewURL, setPreviewURL] = useState("");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!file) {
      setPreviewURL("");
      return;
    }
    const url = URL.createObjectURL(file);
    setPreviewURL(url);
    return () => URL.revokeObjectURL(url);
  }, [file]);

  async function submitUpload() {
    if (!file) {
      setError("Choose an image file first.");
      return;
    }
    if (!reason.trim()) {
      setError("Please enter an operation reason");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const form = new FormData();
      form.set("metadata", JSON.stringify({ command_id: "", reason: reason.trim(), confirm: true }));
      form.set("file", file, file.name);
      const result = await api.uploadServerIcon(form);
      if (result.error) {
        setError(result.error);
        return;
      }
      onDone();
      onClose();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function submitRemove() {
    if (!reason.trim()) {
      setError("Please enter an operation reason");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const result = await api.action("/api/actions/remove-server-icon", { command_id: "", reason: reason.trim(), confirm: true });
      if (result.error) {
        setError(result.error);
        return;
      }
      onDone();
      onClose();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return createPortal(
    <div className="modal-backdrop" role="presentation">
      <section className="modal command-modal" role="dialog" aria-modal="true" aria-label={"Change server icon"}>
        <div className="modal-head">
          <div>
            <div className="eyebrow">{"Server identity"}</div>
            <h2>{"Change server icon"}</h2>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} disabled={busy} aria-label={"Close"}><X size={15} /></button>
        </div>
        <div className="command-body">
          <label className={`gift-file-picker ${file ? "has-file" : ""}`}>
            <input type="file" accept=".png,.jpg,.jpeg,.webp,.gif,image/png,image/jpeg,image/webp,image/gif" onChange={(event) => setFile(event.target.files?.[0] ?? null)} />
            {previewURL ? <img className="gift-file-icon" src={previewURL} alt="" style={{ objectFit: "cover" }} /> : <ImagePlus size={22} />}
            <span className="gift-file-copy"><span className="gift-field-label">{"New icon"}</span><strong>{file ? file.name : "Choose a PNG, JPEG, WebP, or GIF image"}</strong></span>
            <span className="gift-file-action">{file ? "Change file" : "Choose file"}</span>
          </label>
          <label className="gift-reason-field"><span>{"Audit reason"}</span><input value={reason} placeholder={"Briefly describe why the server icon is changing"} onChange={(event) => setReason(event.target.value)} /></label>
          {error && <Alert>{error}</Alert>}
        </div>
        <div className="modal-actions">
          <button className="btn" type="button" onClick={onClose} disabled={busy}>{"Close"}</button>
          {hasIcon && (
            <button className="btn danger icon-text" type="button" onClick={() => void submitRemove()} disabled={busy}>
              {busy ? <Loader2 className="spin" size={15} /> : <Trash2 size={15} />}
              {"Remove icon"}
            </button>
          )}
          <button className="btn primary icon-text" type="button" onClick={() => void submitUpload()} disabled={busy}>
            {busy ? <Loader2 className="spin" size={15} /> : <Upload size={15} />}
            {"Upload icon"}
          </button>
        </div>
      </section>
    </div>,
    document.body
  );
}

// --- Server control (status / restart / update) --------------------------

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// useAdminRestartWatcher backs the "the admin panel is bouncing itself"
// flow after Restart/Update: those actions ask owpengram-server to relaunch
// the admin process once *it* is back up (see internal/procctl's
// PendingAdminRestart), so from the browser's side this just means polling
// /api/session until a *different* boot_id answers -- proof a genuinely new
// process is up, not just that the old one is still slow -- then reloading
// the page. A timeout surfaces as a message with a manual reload button
// instead of spinning forever if something went wrong server-side.
function useAdminRestartWatcher() {
  const [waiting, setWaiting] = useState(false);
  const [timedOut, setTimedOut] = useState(false);
  const cancelled = useRef(false);

  const watch = useCallback(async (timeoutMs = 150000) => {
    cancelled.current = false;
    setTimedOut(false);
    setWaiting(true);
    let baseline = "";
    try {
      baseline = (await api.session()).boot_id ?? "";
    } catch {
      // Falls through to polling anyway -- worst case it reloads on the
      // first boot_id it manages to read, which is still correct.
    }
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      if (cancelled.current) return;
      await sleep(1500);
      try {
        const session = await api.session();
        if (session.boot_id && session.boot_id !== baseline) {
          window.location.reload();
          return;
        }
      } catch {
        // Expected mid-bounce: the old process is dying or the new one
        // hasn't opened its listener yet. Keep polling.
      }
    }
    setWaiting(false);
    setTimedOut(true);
  }, []);

  const dismiss = useCallback(() => {
    cancelled.current = true;
    setWaiting(false);
    setTimedOut(false);
  }, []);

  return { waiting, timedOut, watch, dismiss };
}

function RestartOverlay({ label, timedOut, onDismiss }: { label: string; timedOut: boolean; onDismiss: () => void }) {
  return createPortal(
    <div className="modal-backdrop" role="presentation">
      <section className="modal command-modal restart-overlay" role="dialog" aria-modal="true" aria-label={label}>
        {timedOut ? (
          <div className="command-body restart-overlay-body">
            <Alert>{"The admin panel did not come back within the expected time. It may still be building/restarting -- reload manually in a bit, or check the server logs."}</Alert>
            <div className="gift-table-actions restart-overlay-actions">
              <button className="btn" type="button" onClick={onDismiss}>{"Dismiss"}</button>
              <button className="btn primary" type="button" onClick={() => window.location.reload()}>{"Reload now"}</button>
            </div>
          </div>
        ) : (
          <div className="command-body restart-overlay-body">
            <Loader2 className="spin" size={28} />
            <p>{label}</p>
          </div>
        )}
      </section>
    </div>,
    document.body
  );
}

function ServerControlSection() {
  const [status, setStatus] = useState<ServerStatus | null>(null);
  const [error, setError] = useState("");
  const [overlayLabel, setOverlayLabel] = useState("");
  const restartWatcher = useAdminRestartWatcher();

  async function load() {
    setError("");
    try {
      setStatus(await api.serverStatus());
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  useEffect(() => { void load(); }, []);

  return (
    <section className="section-block">
      <SectionHead
        title={"Process control"}
        text={"Restart rebuilds and relaunches owpengram-server. Update also runs git pull first and rebuilds both binaries. Either way the admin panel bounces onto its (possibly rebuilt) binary too, a few seconds after the server comes back -- this page reloads itself once that's done."}
        action={<button className="btn icon-text" type="button" onClick={() => void load()}><RefreshCw size={15} /> {"Refresh status"}</button>}
      />
      {error && <Alert>{error}</Alert>}
      <div className="card-body">
        {status && (
          <div className="metric-row">
            <Metric label={"owpengram-server"} value={status.ServerAlive ? `running (pid ${status.ServerPID})` : "stopped"} tone={status.ServerAlive ? "good" : "danger"} />
            <Metric label={"admin panel"} value={status.AdminAlive ? `running (pid ${status.AdminPID})` : "stopped"} tone={status.AdminAlive ? "good" : "danger"} />
          </div>
        )}
        <div className="gift-table-actions">
          <ActionButton
            tone="warn"
            label={"Restart server"}
            path="/api/actions/restart-server"
            payload={() => ({})}
            onDone={() => {
              setOverlayLabel("Restarting owpengram-server and the admin panel...");
              void restartWatcher.watch();
            }}
          />
          <ActionButton
            tone="danger"
            label={"Update (git pull + rebuild + restart)"}
            path="/api/actions/update-server"
            payload={() => ({})}
            onDone={() => {
              setOverlayLabel("Pulling, rebuilding, and restarting owpengram-server and the admin panel...");
              void restartWatcher.watch();
            }}
          />
        </div>
      </div>
      {restartWatcher.waiting && <RestartOverlay label={overlayLabel} timedOut={false} onDismiss={restartWatcher.dismiss} />}
      {restartWatcher.timedOut && <RestartOverlay label={overlayLabel} timedOut={true} onDismiss={restartWatcher.dismiss} />}
    </section>
  );
}

// --- .env editor -----------------------------------------------------

function EnvSection() {
  const [groups, setGroups] = useState<EnvGroup[]>([]);
  const [values, setValues] = useState<Record<string, string>>({});
  const [open, setOpen] = useState<Record<string, boolean>>({});
  const [error, setError] = useState("");

  async function load() {
    setError("");
    try {
      const g = await api.serverEnv();
      setGroups(g);
      const next: Record<string, string> = {};
      for (const group of g) {
        for (const field of group.fields) {
          next[field.key] = field.value;
        }
      }
      setValues(next);
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  useEffect(() => { void load(); }, []);

  const fieldCount = useMemo(() => groups.reduce((sum, g) => sum + g.fields.length, 0), [groups]);

  return (
    <section className="section-block">
      <SectionHead title={"Environment (.env)"} text={`${fieldCount} setting(s) across ${groups.length} group(s). Changes take effect on the next Restart/Update.`} />
      {error && <Alert>{error}</Alert>}
      <div className="env-groups">
        {groups.map((group) => {
          const isOpen = !!open[group.title];
          return (
            <div key={group.title} className={`env-group ${isOpen ? "open" : ""}`}>
              <button
                className="env-group-toggle"
                type="button"
                aria-expanded={isOpen}
                onClick={() => setOpen((prev) => ({ ...prev, [group.title]: !prev[group.title] }))}
              >
                <span className="env-group-toggle-text">
                  <span className="env-group-toggle-title">{group.title}</span>
                  <span className="env-group-toggle-count">{`${group.fields.length} field${group.fields.length === 1 ? "" : "s"}`}</span>
                </span>
                <ChevronDown size={16} className="env-group-chevron" />
              </button>
              {isOpen && (
                <div className="env-group-body">
                  {group.description && <p className="env-group-desc">{group.description}</p>}
                  {group.fields.map((field) => (
                    <label key={field.key} className="form-field env-field">
                      <span className="mono">{field.key}</span>
                      {field.description && <span className="env-field-desc">{field.description}</span>}
                      <input
                        type={field.sensitive ? "password" : "text"}
                        value={values[field.key] ?? ""}
                        placeholder={field.default_value}
                        onChange={(event) => setValues((prev) => ({ ...prev, [field.key]: event.target.value }))}
                      />
                    </label>
                  ))}
                </div>
              )}
            </div>
          );
        })}
      </div>
      <div className="gift-table-actions env-save-row">
        <ActionButton
          tone="warn"
          label={"Save .env changes"}
          path="/api/actions/update-server-env"
          payload={() => ({ values })}
          onDone={() => void load()}
        />
      </div>
    </section>
  );
}
