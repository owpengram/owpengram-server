import { ChevronDown, CircleCheck, CircleOff, CircleX, Database, Download, HardDrive, ImageOff, ImagePlus, Layers, Loader2, RefreshCw, Server, ShieldCheck, Trash2, Upload, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { Alert, LoadingSurface, PageFrame, SectionHead } from "../components/ui";
import type { DockerService, EnvGroup, ServerIdentity, ServerStatus } from "../types";

// Server Settings: the web-panel equivalent of tui-panel/server-panel.py's
// menu -- admin-editable server name/description/icon (served to clients
// over /owpengram/server-info + /owpengram/server-icon), .env editing, and
// live process/Docker status + Restart/Update. See
// cmd/telesrv-admin/serversettings.go for the backend.
//
// Split into two tabs: "Settings" (identity + .env, rarely touched, no live
// state) and "Services" (live process/container status + restart/update,
// the operational side someone actually watches while things are moving).
export function ServerSettingsPage() {
  const [tab, setTab] = useState<"settings" | "services">("settings");
  return (
    <PageFrame title={"Server Settings"} eyebrow={"Identity, .env, and live process/service control"}>
      <div className="tab-bar" role="tablist" aria-label={"Server Settings sections"}>
        <button className={`tab-btn ${tab === "settings" ? "active" : ""}`} type="button" role="tab" aria-selected={tab === "settings"} onClick={() => setTab("settings")}>
          {"Settings"}
        </button>
        <button className={`tab-btn ${tab === "services" ? "active" : ""}`} type="button" role="tab" aria-selected={tab === "services"} onClick={() => setTab("services")}>
          {"Services"}
        </button>
      </div>
      {tab === "settings" ? (
        <div className="stacked-sections">
          <IdentitySection />
          <LoginNotificationsSection />
          <EnvSection />
        </div>
      ) : (
        <div className="stacked-sections">
          <ServicesTab />
        </div>
      )}
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
      <SectionHead title={"Server identity"} />
      {error && <Alert>{error}</Alert>}
      {!identity ? (
        <LoadingSurface label={"Loading identity..."} />
      ) : (
        <div className="card-body identity-card">
          <div className="identity-layout">
            <div className="avatar-edit-slot">
              {identity.icon_ext && !iconFailed ? (
                <img
                  className="avatar-photo-img"
                  src={api.serverIconURL() + `&b=${iconBust}`}
                  alt=""
                  style={{ width: 88, height: 88 }}
                  onError={() => setIconFailed(true)}
                />
              ) : (
                <div className="avatar-fallback server-icon-fallback" style={{ width: 88, height: 88 }}>
                  <ImageOff size={26} />
                </div>
              )}
              <button
                className="icon-btn avatar-edit-btn"
                type="button"
                aria-label={"Change server icon"}
                title={"Change server icon"}
                onClick={() => setIconModalOpen(true)}
              >
                <ImagePlus size={14} />
              </button>
            </div>
            <div className="server-identity-fields">
              <label className="form-field"><span>{"Name"}</span><input value={name} maxLength={128} onChange={(event) => setName(event.target.value)} /></label>
              <label className="form-field"><span>{"Description"}</span><textarea rows={4} value={description} maxLength={512} onChange={(event) => setDescription(event.target.value)} /></label>
            </div>
          </div>
          <div className="gift-table-actions identity-save-row">
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

// --- Login notifications ------------------------------------------------

// LoginNotificationsSection edits the 777000 login-notification message's
// per-method (phone/email) template -- a different concern from brand
// identity above even though both live in the same identity.json (see
// cmd/telesrv-admin/serversettings.go's handleSetWelcomeMessageTemplatesAPI
// doc comment), so it gets its own card and its own save action.
function LoginNotificationsSection() {
  const [identity, setIdentity] = useState<ServerIdentity | null>(null);
  const [phoneTemplate, setPhoneTemplate] = useState("");
  const [emailTemplate, setEmailTemplate] = useState("");
  const [codeTemplate, setCodeTemplate] = useState("");
  const [error, setError] = useState("");

  async function load() {
    setError("");
    try {
      const info = await api.serverIdentity();
      setIdentity(info);
      setPhoneTemplate(info.welcome_message_phone_template ?? "");
      setEmailTemplate(info.welcome_message_email_template ?? "");
      setCodeTemplate(info.login_code_message_template ?? "");
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  useEffect(() => { void load(); }, []);

  const phoneIsOverridden = phoneTemplate.trim() !== "";
  const emailIsOverridden = emailTemplate.trim() !== "";
  const codeIsOverridden = codeTemplate.trim() !== "";
  // Mirrors the server-side check in handleSetLoginCodeMessageTemplateAPI --
  // disable the save button instead of letting the operator submit a
  // template that would silently never deliver the actual OTP code.
  const codeOccurrences = (codeTemplate.match(/\{\{code\}\}/g) ?? []).length;
  const codeTemplateInvalid = codeIsOverridden && codeOccurrences !== 1;

  return (
    <section className="section-block">
      <SectionHead title={"Login notifications"} />
      {error && <Alert>{error}</Alert>}
      {!identity ? (
        <LoadingSurface label={"Loading login notification templates..."} />
      ) : (
        <div className="card-body">
          <p style={{ color: "var(--muted)", marginTop: 0 }}>
            {"Sent from the official system account on every completed sign-in. Use "}
            <code>{"{{server_name}}"}</code>
            {" to insert the server's configured name."}
          </p>
          <label className="form-field">
            <span>
              {"Phone sign-in template"}
              {" "}
              {phoneIsOverridden ? <span className="badge good">{"custom"}</span> : <span className="badge">{"default"}</span>}
            </span>
            <textarea
              rows={4}
              value={phoneTemplate}
              onChange={(event) => setPhoneTemplate(event.target.value)}
              placeholder={identity.default_welcome_message_phone_template}
            />
          </label>
          <div className="gift-table-actions">
            <ActionButton
              tone="neutral"
              compact
              label={"Reset to default"}
              path="/api/actions/set-welcome-message-templates"
              payload={() => ({ phone_template: "", email_template: emailTemplate })}
              disabled={!phoneIsOverridden}
              onDone={() => { setPhoneTemplate(""); void load(); }}
            />
          </div>
          <label className="form-field">
            <span>
              {"Email sign-in template"}
              {" "}
              {emailIsOverridden ? <span className="badge good">{"custom"}</span> : <span className="badge">{"default"}</span>}
            </span>
            <textarea
              rows={4}
              value={emailTemplate}
              onChange={(event) => setEmailTemplate(event.target.value)}
              placeholder={identity.default_welcome_message_email_template}
            />
          </label>
          <div className="gift-table-actions">
            <ActionButton
              tone="neutral"
              compact
              label={"Reset to default"}
              path="/api/actions/set-welcome-message-templates"
              payload={() => ({ phone_template: phoneTemplate, email_template: "" })}
              disabled={!emailIsOverridden}
              onDone={() => { setEmailTemplate(""); void load(); }}
            />
          </div>
          <div className="gift-table-actions identity-save-row">
            <ActionButton
              tone="neutral"
              label={"Save login notification templates"}
              path="/api/actions/set-welcome-message-templates"
              payload={() => ({ phone_template: phoneTemplate, email_template: emailTemplate })}
              onDone={() => void load()}
            />
          </div>
          <p style={{ color: "var(--muted)", marginTop: "1.5em", borderTop: "1px solid var(--line)", paddingTop: "1em" }}>
            {"Sent from the official system account with every login code (SMS and email alike). Must contain "}
            <code>{"{{code}}"}</code>
            {" exactly once -- that's where the actual code is inserted and bolded. "}
            <code>{"{{server_name}}"}</code>
            {" is optional and may appear any number of times."}
          </p>
          <label className="form-field">
            <span>
              {"Login-code message template"}
              {" "}
              {codeIsOverridden ? <span className="badge good">{"custom"}</span> : <span className="badge">{"default"}</span>}
            </span>
            <textarea
              rows={5}
              value={codeTemplate}
              onChange={(event) => setCodeTemplate(event.target.value)}
              placeholder={identity.default_login_code_message_template}
            />
            {codeTemplateInvalid && (
              <span style={{ color: "var(--danger-text)", fontSize: "0.85em" }}>
                {codeOccurrences === 0
                  ? "Must contain {{code}} exactly once -- it is currently missing."
                  : `Must contain {{code}} exactly once -- it currently appears ${codeOccurrences} times.`}
              </span>
            )}
          </label>
          <div className="gift-table-actions">
            <ActionButton
              tone="neutral"
              compact
              label={"Reset to default"}
              path="/api/actions/set-login-code-message-template"
              payload={() => ({ template: "" })}
              disabled={!codeIsOverridden}
              onDone={() => { setCodeTemplate(""); void load(); }}
            />
          </div>
          <div className="gift-table-actions identity-save-row">
            <ActionButton
              tone="neutral"
              label={"Save login-code message template"}
              path="/api/actions/set-login-code-message-template"
              payload={() => ({ template: codeTemplate })}
              disabled={codeTemplateInvalid}
              onDone={() => void load()}
            />
          </div>
        </div>
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

// --- Services tab (live Docker + process status, restart/update) --------

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

type LiveTone = "good" | "warn" | "danger" | "idle";

function liveDotIcon(tone: LiveTone) {
  switch (tone) {
    case "good": return <CircleCheck size={15} />;
    case "warn": return <Loader2 className="spin" size={15} />;
    case "danger": return <CircleX size={15} />;
    default: return <CircleOff size={15} />;
  }
}

// ServiceCard renders one live status tile -- a Docker container or a local
// process -- with a status pill (dot + label) and up to one detail line.
// Shared between the Docker services grid and the process-control grid so
// both read the same way at a glance instead of two different layouts.
function ServiceCard({
  icon,
  name,
  tone,
  statusLabel,
  detail
}: {
  icon: React.ReactNode;
  name: string;
  tone: LiveTone;
  statusLabel: string;
  detail?: string;
}) {
  return (
    <div className={`service-card tone-${tone}`}>
      <div className="service-card-icon">{icon}</div>
      <div className="service-card-body">
        <div className="service-card-name">{name}</div>
        <div className="service-card-detail">{detail ?? " "}</div>
      </div>
      <div className="service-card-status">
        {liveDotIcon(tone)}
        <span>{statusLabel}</span>
      </div>
    </div>
  );
}

const dockerServiceIcon: Record<string, React.ReactNode> = {
  postgres: <Database size={18} />,
  redis: <Layers size={18} />,
  minio: <HardDrive size={18} />
};

function dockerTone(service: DockerService): LiveTone {
  const state = service.state.toLowerCase();
  const health = service.health.toLowerCase();
  if (state !== "running") return "danger";
  if (health === "unhealthy") return "danger";
  if (health === "starting") return "warn";
  return "good";
}

function dockerStatusLabel(service: DockerService): string {
  const state = service.state.toLowerCase();
  if (state !== "running") return service.state || "stopped";
  if (service.health) return service.health;
  return "running";
}

// Live polling cadence for the Services tab. Fast enough that a
// Restart/Update's effect on the process/container cards feels immediate,
// slow enough not to hammer `docker compose ps` (which shells out) every
// couple seconds for no reason.
const LIVE_POLL_MS = 4000;

// UpdateButton is a two-state control: "Check updates" (a plain git fetch +
// commit count, no side effects) until a check finds the branch behind its
// upstream, at which point it becomes "Update (<N>)" -- a second click runs
// the real git pull + rebuild + restart (via the existing update-server
// action, called directly with confirm:true since the check step already
// serves as the "are you sure" gate). Auto-checks once on mount so the
// button reflects reality without an operator having to click twice.
function UpdateButton({ onUpdateStarted }: { onUpdateStarted: (overlayLabel: string) => void }) {
  const [behind, setBehind] = useState<number | null>(null);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const check = useCallback(async () => {
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const result = await api.checkServerUpdates();
      setBehind(result.commits_behind);
      setMessage(result.commits_behind > 0
        ? `${result.commits_behind} new commit${result.commits_behind === 1 ? "" : "s"} pulled from GitHub.`
        : "Already up to date.");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }, []);

  useEffect(() => { void check(); }, [check]);

  async function runUpdate() {
    const commits = behind ?? 0;
    if (!window.confirm(`Pull ${commits} commit(s), rebuild, and restart owpengram-server and the admin panel?`)) {
      return;
    }
    setBusy(true);
    setError("");
    try {
      const result = await api.action("/api/actions/update-server", { command_id: "", reason: "Update via Services tab", confirm: true });
      if (result.error) {
        setError(result.error);
        setBusy(false);
        return;
      }
      onUpdateStarted(`Pulled ${commits} commit${commits === 1 ? "" : "s"} -- rebuilding and restarting owpengram-server and the admin panel...`);
    } catch (err) {
      setError(errorMessage(err));
      setBusy(false);
    }
  }

  const hasUpdates = (behind ?? 0) > 0;

  return (
    <button
      className={`btn compact-btn icon-text ${hasUpdates ? "danger" : ""}`}
      type="button"
      disabled={busy}
      title={error || message || undefined}
      onClick={() => void (hasUpdates ? runUpdate() : check())}
    >
      {busy ? <Loader2 className="spin" size={15} /> : hasUpdates ? <Download size={15} /> : <RefreshCw size={15} />}
      {hasUpdates ? `Update (${behind})` : "Check updates"}
    </button>
  );
}

function ServicesTab() {
  const [status, setStatus] = useState<ServerStatus | null>(null);
  const [statusError, setStatusError] = useState("");
  const [docker, setDocker] = useState<DockerService[] | null>(null);
  const [dockerError, setDockerError] = useState("");
  const [overlayLabel, setOverlayLabel] = useState("");
  const restartWatcher = useAdminRestartWatcher();
  const pausedRef = useRef(false);
  pausedRef.current = restartWatcher.waiting;

  const load = useCallback(async () => {
    if (pausedRef.current) return;
    try {
      setStatus(await api.serverStatus());
      setStatusError("");
    } catch (err) {
      setStatusError(errorMessage(err));
    }
    try {
      setDocker(await api.dockerStatus());
      setDockerError("");
    } catch (err) {
      setDockerError(errorMessage(err));
    }
  }, []);

  useEffect(() => {
    void load();
    const id = window.setInterval(() => void load(), LIVE_POLL_MS);
    return () => window.clearInterval(id);
  }, [load]);

  const loading = status === null && docker === null && !statusError && !dockerError;

  return (
    <>
      <section className="section-block">
        <SectionHead
          title={"Services"}
          action={
            <div className="services-header-actions">
              <UpdateButton
                onUpdateStarted={(label) => {
                  setOverlayLabel(label);
                  void restartWatcher.watch();
                }}
              />
              <ActionButton
                compact
                tone="primary"
                label={"Restart"}
                path="/api/actions/restart-server"
                payload={() => ({})}
                onDone={() => {
                  setOverlayLabel("Restarting owpengram-server and the admin panel...");
                  void restartWatcher.watch();
                }}
              />
            </div>
          }
        />
        {statusError && <Alert>{statusError}</Alert>}
        {dockerError && <Alert>{dockerError}</Alert>}
        {loading ? (
          <LoadingSurface label={"Loading service status..."} />
        ) : (
          <div className="service-grid">
            {status && (
              <>
                <ServiceCard
                  icon={<Server size={18} />}
                  name={"Server"}
                  tone={status.ServerAlive ? "good" : "danger"}
                  statusLabel={status.ServerAlive ? "running" : "stopped"}
                  detail={status.ServerAlive ? `pid ${status.ServerPID}` : undefined}
                />
                <ServiceCard
                  icon={<ShieldCheck size={18} />}
                  name={"admin panel"}
                  tone={status.AdminAlive ? "good" : "danger"}
                  statusLabel={status.AdminAlive ? "running" : "stopped"}
                  detail={status.AdminAlive ? `pid ${status.AdminPID}` : undefined}
                />
              </>
            )}
            {docker?.map((service) => (
              <ServiceCard
                key={service.name}
                icon={dockerServiceIcon[service.name] ?? <Database size={18} />}
                name={service.name}
                tone={dockerTone(service)}
                statusLabel={dockerStatusLabel(service)}
                detail={service.state}
              />
            ))}
          </div>
        )}
      </section>
      {restartWatcher.waiting && <RestartOverlay label={overlayLabel} timedOut={false} onDismiss={restartWatcher.dismiss} />}
      {restartWatcher.timedOut && <RestartOverlay label={overlayLabel} timedOut={true} onDismiss={restartWatcher.dismiss} />}
    </>
  );
}
