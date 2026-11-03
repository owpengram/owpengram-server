import {
  ArrowsRotateRight,
  BroadcastSignal,
  Database,
  Envelope,
  FaceRobot,
  Fingerprint,
  Gear,
  Globe,
  Handset,
  HardDrive,
  Key,
  Link as LinkIcon,
  MagicWand,
  Magnifier,
  MapPin,
  PaperPlane,
  PersonGear,
  Pulse,
  Server,
  ShieldCheck,
  Smartphone,
  Star,
  Wallet
} from "@gravity-ui/icons";
import { ChevronDown, CircleCheck, CircleOff, CircleX, Download, ImageOff, ImagePlus, Loader2, RefreshCw, Trash2, Upload, X } from "../components/icons";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { Meter, Sparkline } from "../components/charts";
import { StorageSettings, isStorageGroup } from "../components/StorageSettings";
import { useServerUpdate } from "../components/pageHeader";
import { Alert, LoadingSurface, PageFrame, SectionHead } from "../components/ui";
import { formatBytes } from "../lib/format";
import type { DashboardResponse, EnvGroup, ServerIdentity, ServerStatus } from "../types";

// Server Settings: identity, login messages, every .env group and the live
// status/restart/update controls. See cmd/telesrv-admin/serversettings.go for
// the backend.
//
// Laid out like a settings app: a list of sections on the left, one section
// on the right. Each .env group is its own section with its own Save, which
// sends only the fields that changed -- the old single list of every key with
// one Save at the bottom made a one-field change a scroll-and-hunt.
type SettingsSection = "status" | "identity" | "messages" | `env:${string}`;

export function ServerSettingsPage() {
  // Status first: it is what the overview's "Update now" links to.
  const [section, setSection] = useState<SettingsSection>("status");
  const [groups, setGroups] = useState<EnvGroup[]>([]);
  const [envError, setEnvError] = useState("");
  const [search, setSearch] = useState("");

  const loadEnv = useCallback(async () => {
    setEnvError("");
    try {
      setGroups(await api.serverEnv());
    } catch (err) {
      setEnvError(errorMessage(err));
    }
  }, []);

  useEffect(() => { void loadEnv(); }, [loadEnv]);

  const needle = search.trim().toLowerCase();
  // A search looks through every .env field (key, description) and shows the
  // matches together, whatever group they live in.
  const matches = useMemo(() => {
    if (!needle) return [];
    return groups
      .map((group) => ({
        ...group,
        fields: group.fields.filter(
          (f) => f.key.toLowerCase().includes(needle) || f.description.toLowerCase().includes(needle) || group.title.toLowerCase().includes(needle)
        )
      }))
      .filter((group) => group.fields.length > 0);
  }, [groups, needle]);

  const activeGroup = section.startsWith("env:") ? groups.find((g) => `env:${g.title}` === section) : undefined;

  return (
    <PageFrame title={"Settings"}>
      <div className="settings-layout">
        <nav className="settings-nav" aria-label={"Settings sections"}>
          <label className="searchbox settings-search">
            <Magnifier />
            <input value={search} onChange={(event) => setSearch(event.target.value)} placeholder={"Search settings"} aria-label={"Search settings"} />
          </label>
          <SettingsNavItem icon={<Pulse />} label={"Status"} active={!needle && section === "status"} onClick={() => { setSearch(""); setSection("status"); }} />
          <SettingsNavItem icon={<Server />} label={"Identity"} active={!needle && section === "identity"} onClick={() => { setSearch(""); setSection("identity"); }} />
          <SettingsNavItem icon={<Envelope />} label={"Login messages"} active={!needle && section === "messages"} onClick={() => { setSearch(""); setSection("messages"); }} />
          {groups.length > 0 && <div className="settings-nav-label">{"Configuration"}</div>}
          {groups.map((group) => (
            <SettingsNavItem
              key={group.title}
              icon={envGroupIcon(group.title)}
              label={group.title}
              count={group.fields.length}
              active={!needle && section === `env:${group.title}`}
              onClick={() => { setSearch(""); setSection(`env:${group.title}`); }}
            />
          ))}
        </nav>

        <div className="settings-content">
          {envError && <Alert>{envError}</Alert>}
          {needle ? (
            matches.length === 0 ? (
              <p className="about-text">{`Nothing in the configuration matches "${search.trim()}".`}</p>
            ) : (
              matches.map((group) => <EnvGroupPanel key={group.title} group={group} onSaved={loadEnv} showTitle />)
            )
          ) : section === "status" ? (
            <ServicesTab />
          ) : section === "identity" ? (
            <IdentitySection />
          ) : section === "messages" ? (
            <LoginNotificationsSection />
          ) : activeGroup && isStorageGroup(activeGroup) ? (
            <StorageSettings group={activeGroup} onSaved={loadEnv} />
          ) : activeGroup ? (
            <EnvGroupPanel key={activeGroup.title} group={activeGroup} onSaved={loadEnv} showTitle />
          ) : (
            <LoadingSurface label={"Loading settings..."} />
          )}
        </div>
      </div>
    </PageFrame>
  );
}

function SettingsNavItem({ icon, label, count, active, onClick }: { icon?: React.ReactNode; label: string; count?: number; active: boolean; onClick: () => void }) {
  return (
    <button type="button" className={`settings-nav-item ${active ? "active" : ""}`.trim()} onClick={onClick} aria-current={active ? "page" : undefined} title={label}>
      {icon && <span className="settings-nav-icon">{icon}</span>}
      <span className="settings-nav-text">{label}</span>
      {count !== undefined && <span className="settings-nav-count">{count}</span>}
    </button>
  );
}

// An icon per .env group, matched on its title (the titles come from the
// server's .env.example sections). Unknown groups get a gear.
const ENV_GROUP_ICONS: [RegExp, React.ReactNode][] = [
  [/server|network/i, <Server />],
  [/phone|sms/i, <Smartphone />],
  [/smtp|outgoing email/i, <PaperPlane />],
  [/email/i, <Envelope />],
  [/link|branding/i, <LinkIcon />],
  [/admin/i, <PersonGear />],
  [/bot/i, <FaceRobot />],
  [/database|postgres/i, <Database />],
  [/web listener|public web/i, <Globe />],
  [/openid|telegram login/i, <Key />],
  [/passkey|webauthn/i, <Fingerprint />],
  [/call/i, <Handset />],
  [/stream/i, <BroadcastSignal />],
  [/map/i, <MapPin />],
  [/\bai\b/i, <MagicWand />],
  [/moderation|verification/i, <ShieldCheck />],
  [/storage|media/i, <HardDrive />],
  [/premium|stars/i, <Star />],
  [/update/i, <ArrowsRotateRight />],
  [/donation|crypto|wallet/i, <Wallet />]
];

function envGroupIcon(title: string): React.ReactNode {
  return ENV_GROUP_ICONS.find(([re]) => re.test(title))?.[1] ?? <Gear />;
}

const isBoolValue = (v: string) => v === "true" || v === "false";

// EnvGroupPanel edits one .env group. Its Save sends only the keys whose value
// differs from what was loaded, so saving one group never rewrites another.
function EnvGroupPanel({ group, onSaved, showTitle }: { group: EnvGroup; onSaved: () => void; showTitle?: boolean }) {
  const initial = useMemo(() => Object.fromEntries(group.fields.map((f) => [f.key, f.value])), [group]);
  const [values, setValues] = useState<Record<string, string>>(initial);
  useEffect(() => setValues(initial), [initial]);

  const changed = useMemo(() => {
    const out: Record<string, string> = {};
    for (const f of group.fields) {
      if ((values[f.key] ?? "") !== (initial[f.key] ?? "")) out[f.key] = values[f.key] ?? "";
    }
    return out;
  }, [values, initial, group]);
  const changedCount = Object.keys(changed).length;

  return (
    <section className="section-block settings-panel">
      {showTitle && <SectionHead title={group.title} text={group.description || undefined} />}
      <div className="settings-fields">
        {group.fields.map((field) => {
          const value = values[field.key] ?? "";
          const effective = value || field.default_value;
          const toggle = isBoolValue(effective) && (isBoolValue(field.default_value) || isBoolValue(value));
          const dirty = field.key in changed;
          return (
            <div key={field.key} className={`settings-field ${dirty ? "dirty" : ""}`.trim()}>
              <div className="settings-field-text">
                <span className="settings-field-key mono">{field.key}</span>
                {field.description && <span className="settings-field-desc">{field.description}</span>}
              </div>
              <div className="settings-field-control">
                {toggle ? (
                  <label className="switch">
                    <input
                      type="checkbox"
                      checked={effective === "true"}
                      onChange={(event) => setValues((prev) => ({ ...prev, [field.key]: event.target.checked ? "true" : "false" }))}
                      aria-label={field.key}
                    />
                    <span className="switch-track" aria-hidden="true" />
                  </label>
                ) : (
                  // These are server settings, never this operator's login:
                  // a text field followed by a password field reads to the
                  // browser and password managers as a sign-in form, and
                  // they filled in the saved console username/password
                  // (which then also showed up as unsaved changes).
                  // "new-password" is the value browsers honour for
                  // password fields; the data-* opt-outs cover 1Password,
                  // LastPass and Bitwarden.
                  <input
                    type={field.sensitive ? "password" : "text"}
                    name={`env-${field.key}`}
                    value={value}
                    placeholder={field.default_value || "not set"}
                    onChange={(event) => setValues((prev) => ({ ...prev, [field.key]: event.target.value }))}
                    aria-label={field.key}
                    autoComplete={field.sensitive ? "new-password" : "off"}
                    autoCorrect="off"
                    autoCapitalize="off"
                    spellCheck={false}
                    data-1p-ignore=""
                    data-lpignore="true"
                    data-bwignore="true"
                  />
                )}
              </div>
            </div>
          );
        })}
      </div>
      <div className="settings-save-row">
        <span className="settings-save-note">
          {changedCount > 0
            ? `${changedCount} unsaved change${changedCount === 1 ? "" : "s"} · applies on the next restart`
            : "Saved to .env; changes apply on the next restart."}
        </span>
        {changedCount > 0 && (
          <button className="btn" type="button" onClick={() => setValues(initial)}>{"Discard"}</button>
        )}
        <ActionButton
          tone="warn"
          label={changedCount > 0 ? `Save ${changedCount} change${changedCount === 1 ? "" : "s"}` : "Save"}
          path="/api/actions/update-server-env"
          payload={() => ({ values: changed })}
          disabled={changedCount === 0}
          onDone={onSaved}
        />
      </div>
    </section>
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

// autoReason skips the "why is this changing" prompt in favor of a fixed
// reason -- for the first-run wizard, where there is no prior state to
// justify changing away from and no one else's icon to be overwriting.
export function ServerIconModal({ hasIcon, onClose, onDone, autoReason }: { hasIcon: boolean; onClose: () => void; onDone: () => void; autoReason?: string }) {
  const [file, setFile] = useState<File | null>(null);
  const [previewURL, setPreviewURL] = useState("");
  const [typedReason, setTypedReason] = useState("");
  const reason = autoReason ?? typedReason;
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
          {autoReason === undefined && (
            <label className="gift-reason-field"><span>{"Audit reason"}</span><input value={typedReason} placeholder={"Briefly describe why the server icon is changing"} onChange={(event) => setTypedReason(event.target.value)} /></label>
          )}
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

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// useAdminRestartWatcher backs the "the admin panel is bouncing itself"
// flow after Restart/Update/saving the storage step: those actions stop the
// server and make the admin process exit, and owpengram-ctl starts both again,
// so from the browser's side this just means polling /api/session until a
// *different* boot_id answers -- proof a genuinely new process is up, not just
// that the old one is still slow -- and the server it started is listening
// (server_ready), then reloading the page. A timeout surfaces as a message with a manual reload button
// instead of spinning forever if something went wrong server-side.
// Known owpengram-server startup log lines, mapped to an operator-facing
// label -- the raw zap lines (phase=reactions elapsed=...) are meant for
// logs/grep, not for someone watching a restart happen. An unrecognized
// line still shows verbatim rather than being dropped, so a future log line
// nobody added a mapping for yet doesn't just vanish from the view.
const STARTUP_LOG_STEPS: { match: RegExp; label: string }[] = [
  { match: /telesrv starting/, label: "Starting server process" },
  { match: /PostgreSQL schema migrated/, label: "Applying database migrations" },
  { match: /persistence dependencies ready/, label: "Connecting to Postgres" },
  { match: /blob backend ready/, label: "Connecting to media storage" },
  { match: /media seed phase complete phase=reactions/, label: "Seeded reactions" },
  { match: /media seed phase complete phase=sticker_sets/, label: "Seeded sticker sets" },
  { match: /media seed phase complete phase=effects/, label: "Seeded message effects" },
  { match: /media seed phase complete phase=emoji_group_icons/, label: "Seeded emoji group icons" },
  { match: /telesrv service ready/, label: "Server is up and serving" }
];

function friendlyStartupLogLine(line: string): string {
  for (const step of STARTUP_LOG_STEPS) {
    if (step.match.test(line)) return step.label;
  }
  return line;
}

export function useAdminRestartWatcher() {
  const [waiting, setWaiting] = useState(false);
  const [timedOut, setTimedOut] = useState(false);
  const [logLines, setLogLines] = useState<string[]>([]);
  const cancelled = useRef(false);

  // 5 minutes: a new server may first have to start the embedded PostgreSQL
  // (downloaded on its very first run), apply pending migrations and, on a
  // fresh install, run the one-time media seed before it listens. A restart
  // that genuinely hangs still surfaces via RestartOverlay's dismiss/reload
  // controls, so a longer timeout only costs patience, never a stuck UI.
  const watch = useCallback(async (timeoutMs = 300000, options?: { beforeReload?: () => Promise<void> | void }) => {
    cancelled.current = false;
    setTimedOut(false);
    setWaiting(true);
    setLogLines([]);
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
        if (session.boot_id && session.boot_id !== baseline && session.server_ready !== false) {
          // beforeReload runs against the new process (this session read
          // already proved it's up) and can't fail the reload -- a reload
          // an operator is staring at a spinner for shouldn't hang on it.
          if (options?.beforeReload) {
            await Promise.resolve(options.beforeReload()).catch(() => undefined);
          }
          // A restarted server is worth landing on the dashboard for,
          // regardless of which tab was open when Restart/Update/Finish
          // setup was clicked -- same replaceState-then-reload pattern
          // LoginPage's onLogin uses for the same reason. replaceState
          // (not pushState) so this doesn't add a "the tab that was open
          // before the restart" entry to browser history either.
          window.history.replaceState(null, "", "/");
          window.location.reload();
          return;
        }
      } catch {
        // Expected mid-bounce: the old process is dying or the new one
        // hasn't opened its listener yet. Keep polling.
      }
      // Best-effort progress view of the *new* owpengram-server's own log --
      // read by the admin process still answering this poll, a separate OS
      // process from whatever is starting up. A failure here (log not
      // written yet, admin process itself mid-bounce) just leaves the last
      // known lines on screen instead of clearing them.
      try {
        const log = await api.serverStartupLog();
        if (log.lines && log.lines.length > 0) {
          setLogLines(log.lines.map(friendlyStartupLogLine));
        }
      } catch {
        // Ignored -- see above.
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

  return { waiting, timedOut, watch, dismiss, logLines };
}

// No fixed detail line under the heading on purpose -- "restarting
// owpengram-server and the admin panel" told the operator nothing they
// didn't already know from having just clicked Restart/Update/Finish setup.
// logLines (from useAdminRestartWatcher, polled off the new process's own
// log) fills that spot instead once there's something real to say -- most
// of a restart's wall-clock time is the new owpengram-server working
// through migrations and, on a fresh install, the one-time media seed, and
// an operator staring at a plain spinner for that long has no way to tell
// "still working" from "stuck". Only the *current* step is shown by
// default -- "Connecting to Postgres", not a scrolling transcript of every
// step so far -- with the full history one click away behind "Show full
// log" for whoever actually wants it, same collapsed-by-default pattern
// the .env editor's groups use.
export function RestartOverlay({ timedOut, onDismiss, logLines }: { timedOut: boolean; onDismiss: () => void; logLines?: string[] }) {
  const [logExpanded, setLogExpanded] = useState(false);
  const hasLog = !!logLines && logLines.length > 0;
  // Before the new process has written its first log line at all -- the
  // built-in PostgreSQL may still be starting (downloaded on its very first
  // run) -- a blank spinner with no text reads as stuck rather than "still
  // working". Say so explicitly instead of showing nothing.
  const currentStatus = hasLog ? logLines[logLines.length - 1] : (timedOut ? undefined : "Starting...");
  const log = hasLog && logExpanded && (
    <ul className="restart-overlay-log">
      {logLines.map((line, index) => <li key={index}>{line}</li>)}
    </ul>
  );
  const logToggle = hasLog && logLines.length > 1 && (
    <button
      className="env-group-toggle restart-overlay-log-toggle"
      type="button"
      aria-expanded={logExpanded}
      onClick={() => setLogExpanded((prev) => !prev)}
    >
      <span className="env-group-toggle-text">{logExpanded ? "Hide full log" : "Show full log"}</span>
      <ChevronDown size={16} className={`env-group-chevron ${logExpanded ? "open" : ""}`} />
    </button>
  );
  return createPortal(
    <div className="modal-backdrop" role="presentation">
      <section className="modal command-modal restart-overlay" role="dialog" aria-modal="true" aria-label={timedOut ? "Restart is taking longer than expected" : "Restarting"}>
        {timedOut ? (
          <div className="command-body restart-overlay-body">
            <div className="restart-overlay-badge warn">
              <RefreshCw size={26} />
            </div>
            <h2 className="restart-overlay-heading">{"Still restarting..."}</h2>
            <Alert>{"The admin panel did not come back within the expected time. It may still be building/restarting -- reload manually in a bit, or check the server logs."}</Alert>
            {currentStatus && <p className="restart-overlay-status">{currentStatus}</p>}
            {logToggle}
            {log}
            <div className="gift-table-actions restart-overlay-actions">
              <button className="btn" type="button" onClick={onDismiss}>{"Dismiss"}</button>
              <button
                className="btn primary"
                type="button"
                onClick={() => {
                  window.history.replaceState(null, "", "/");
                  window.location.reload();
                }}
              >
                {"Reload now"}
              </button>
            </div>
          </div>
        ) : (
          <div className="command-body restart-overlay-body">
            <div className="restart-overlay-badge">
              <RefreshCw size={26} className="restart-overlay-spin" />
            </div>
            <h2 className="restart-overlay-heading">{"Restarting"}</h2>
            <div className="loader-bar restart-overlay-progress" />
            {currentStatus && <p className="restart-overlay-status">{currentStatus}</p>}
            {logToggle}
            {log}
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

// ServiceCard renders one live status tile -- a local process -- with a
// status pill (dot + label) and up to one detail line.
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

// Live polling cadence for the Services tab. Fast enough that a
// Restart/Update's effect on the process cards feels immediate.
const LIVE_POLL_MS = 4000;

// UpdateButton is a two-state control: "Check updates" (a plain git fetch +
// commit count, no side effects) until a check finds the branch behind its
// upstream, at which point it becomes "Update (<N>)" -- an ActionButton
// running the real git pull + rebuild + restart through the same
// reason/dry-run/confirm flow as Restart beside it. The check is not the
// gate: it only reports how far behind the branch is, and pulling somebody
// else's commits and relaunching the deployment deserves the same recorded
// reason as every other action here. Auto-checks once on mount so the button
// reflects reality without an operator having to click twice.
function UpdateButton({ onUpdateStarted }: { onUpdateStarted: () => void }) {
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

  const commits = behind ?? 0;
  const hasUpdates = commits > 0;

  if (hasUpdates) {
    return (
      <ActionButton
        compact
        tone="danger"
        icon={<Download size={15} />}
        label={`Update (${commits})`}
        path="/api/actions/update-server"
        payload={() => ({})}
        onDone={onUpdateStarted}
      />
    );
  }

  return (
    <button
      className="btn compact-btn icon-text"
      type="button"
      disabled={busy}
      title={error || message || undefined}
      onClick={() => void check()}
    >
      {busy ? <Loader2 className="spin" size={15} /> : <RefreshCw size={15} />}
      {"Check updates"}
    </button>
  );
}

function ServicesTab() {
  const [status, setStatus] = useState<ServerStatus | null>(null);
  const [statusError, setStatusError] = useState("");
  const [host, setHost] = useState<DashboardResponse | null>(null);
  const restartWatcher = useAdminRestartWatcher();
  const { update } = useServerUpdate();
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
    // Host load comes with the overview's data; a failure just hides it.
    api.dashboard().then(setHost).catch(() => undefined);
  }, []);

  useEffect(() => {
    void load();
    const id = window.setInterval(() => void load(), LIVE_POLL_MS);
    return () => window.clearInterval(id);
  }, [load]);

  const loading = status === null && !statusError;
  const allUp = Boolean(status?.ServerAlive && status?.AdminAlive);
  const snap = host?.host;
  const history = host?.host_history ?? [];
  const memPct = snap?.Ready && snap.MemTotalBytes > 0 ? (snap.MemUsedBytes / snap.MemTotalBytes) * 100 : 0;
  const diskPct = snap?.Ready && snap.DiskReady && snap.DiskTotalBytes > 0 ? ((snap.DiskTotalBytes - snap.DiskFreeBytes) / snap.DiskTotalBytes) * 100 : 0;
  const releaseUpdate = update?.available ? update.latest : undefined;

  return (
    <>
      {statusError && <Alert>{statusError}</Alert>}
      {loading ? (
        <LoadingSurface label={"Loading service status..."} />
      ) : (
        <>
          <section className={`status-hero ${allUp ? "up" : "down"}`}>
            <span className="status-hero-dot" aria-hidden="true" />
            <div className="status-hero-text">
              <strong>{allUp ? "Everything is running" : status?.ServerAlive ? "The admin panel is not running" : "The server is stopped"}</strong>
              {releaseUpdate && <span>{`${releaseUpdate} is available`}</span>}
            </div>
            <div className="status-hero-actions">
              <UpdateButton onUpdateStarted={() => void restartWatcher.watch()} />
              <ActionButton
                compact
                tone="primary"
                label={"Restart"}
                path="/api/actions/restart-server"
                payload={() => ({})}
                onDone={() => void restartWatcher.watch()}
              />
            </div>
          </section>

          {status && (
            <section className="section-block settings-panel">
              <SectionHead title={"Processes"} />
              <div className="process-list">
                <ProcessRow name={"Server"} detail={"MTProto, web client, public pages"} alive={status.ServerAlive} pid={status.ServerPID} />
                <ProcessRow name={"Admin panel"} detail={"This console"} alive={status.AdminAlive} pid={status.AdminPID} />
                <ProcessRow name={"Supervisor"} detail={"owpengram-ctl, restarts the others"} alive={status.CtlAlive} pid={status.CtlPID} />
              </div>
            </section>
          )}

          {snap?.Ready && (
            <div className="status-host">
              <section className="overview-card">
                <header className="overview-card-head">
                  <h2>{"CPU"}</h2>
                  <span className="overview-value">{`${snap.CPUPercent.toFixed(0)}%`}</span>
                </header>
                <Sparkline values={history.map((s) => s.cpu)} max={100} height={52} label={"CPU load, last 15 minutes"} />
              </section>
              <section className="overview-card">
                <header className="overview-card-head">
                  <h2>{"Memory"}</h2>
                  <span className="overview-value">{`${formatBytes(String(snap.MemUsedBytes))} / ${formatBytes(String(snap.MemTotalBytes))}`}</span>
                </header>
                <Sparkline values={history.map((s) => s.mem)} max={snap.MemTotalBytes || undefined} height={52} tone={memPct >= 90 ? "warn" : "brand"} label={"Memory used, last 15 minutes"} />
              </section>
              <section className="overview-card">
                <header className="overview-card-head">
                  <h2>{"Disk"}</h2>
                  <span className="overview-value">{snap.DiskReady ? `${formatBytes(String(snap.DiskFreeBytes))} free` : "—"}</span>
                </header>
                <Meter percent={diskPct} />
                <span className="overview-card-hint">{snap.DiskReady ? `${diskPct.toFixed(0)}% of ${formatBytes(String(snap.DiskTotalBytes))} used` : "No reading yet"}</span>
              </section>
            </div>
          )}
        </>
      )}
      {restartWatcher.waiting && <RestartOverlay timedOut={false} onDismiss={restartWatcher.dismiss} logLines={restartWatcher.logLines} />}
      {restartWatcher.timedOut && <RestartOverlay timedOut={true} onDismiss={restartWatcher.dismiss} logLines={restartWatcher.logLines} />}
    </>
  );
}

function ProcessRow({ name, detail, alive, pid }: { name: string; detail: string; alive: boolean; pid: number }) {
  return (
    <div className={`process-row ${alive ? "up" : "down"}`}>
      <span className="process-dot" aria-hidden="true" />
      <div className="process-text">
        <strong>{name}</strong>
        <span>{detail}</span>
      </div>
      <span className="process-pid mono">{alive && pid ? `pid ${pid}` : ""}</span>
      <span className={`badge ${alive ? "good" : "danger"}`}>{alive ? "Running" : "Stopped"}</span>
    </div>
  );
}
