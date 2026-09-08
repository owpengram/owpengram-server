import { ArrowRight, Check, ImagePlus, Loader2, Rocket, UserPlus } from "lucide-react";
import type { ReactNode } from "react";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import { RestartOverlay, ServerIconModal, useAdminRestartWatcher } from "../pages/ServerSettingsPage";
import { ThemeSwitch } from "../theme";
import { AppBackground } from "./AppBackground";
import { Alert } from "./ui";

// The first-run wizard: shown instead of the normal shell exactly once, when
// GET /api/session answers setup_completed=false (see
// identity.Store.SetupPending and cmd/telesrv-admin/server.go's
// handleSession). It covers the same ground
// tui-panel/server-panel.py's SetupWizardScreen used to ask for in the
// terminal before the server could even start -- server identity, the public
// network fields a real deployment needs, and a named operator account to
// replace the generated break-glass password -- except every field here
// already has a working default (see quickstart's bootstrap_env), so nothing
// blocks Start anymore. This just walks through customizing it.
//
// Every step's "Continue" calls the same /api/actions/* route the equivalent
// Server Settings / Operators screen uses, with a fixed reason instead of an
// operator-typed one and confirm:true immediately (no dry-run screen). The
// rest of the panel asks an operator to justify a change to a live,
// populated deployment; here the operator IS the only account that has ever
// existed, configuring a server nobody else is using yet -- asking "why are
// you naming your own server" is friction with no audit value.
const WIZARD_REASON = "Set from the first-run setup wizard";

type StepId = "welcome" | "identity" | "network" | "account" | "done";

const STEP_ORDER: StepId[] = ["welcome", "identity", "network", "account", "done"];
const STEP_LABEL: Record<StepId, string> = {
  welcome: "Welcome",
  identity: "Identity",
  network: "Network",
  account: "Account",
  done: "Done"
};

// No prop for "leave the wizard early": this only ever shows on a genuinely
// first-ever start, before there is a real deployment for a Skip to defer
// anything about -- see the Welcome step. The only way out is Done's
// "Finish setup & restart", which reloads the page once the new process
// answers; that reload is what dismisses this component (a fresh
// /api/session comes back with setup_completed=true).
export function SetupWizard() {
  const [step, setStep] = useState<StepId>("welcome");

  function goTo(next: StepId) {
    setStep(next);
  }

  return (
    <main className="login-page setup-wizard-page">
      <AppBackground />
      <section className="login-panel setup-wizard-panel">
        <div className="login-head">
          <div className="brand brand-elevated">
            <span>
              <strong>{"Let's set up your server"}</strong>
              <small>{"First-run setup"}</small>
            </span>
          </div>
          <div className="login-head-actions">
            <ThemeSwitch />
          </div>
        </div>

        <div className="wizard-steps">
          {STEP_ORDER.map((id, index) => {
            const currentIndex = STEP_ORDER.indexOf(step);
            const state = index === currentIndex ? "active" : index < currentIndex ? "done" : "";
            return (
              <div key={id} className={`command-step ${state}`}>
                <span>{index < currentIndex ? <Check size={12} /> : index + 1}</span>
                <strong>{STEP_LABEL[id]}</strong>
              </div>
            );
          })}
        </div>

        {step === "welcome" && <WelcomeStep onNext={() => goTo("identity")} />}
        {step === "identity" && <IdentityStep onNext={() => goTo("network")} />}
        {step === "network" && <NetworkStep onNext={() => goTo("account")} />}
        {step === "account" && <AccountStep onNext={() => goTo("done")} />}
        {step === "done" && <DoneStep />}
      </section>
    </main>
  );
}

function WizardActions({ children }: { children: ReactNode }) {
  return <div className="wizard-actions">{children}</div>;
}

function WelcomeStep({ onNext }: { onNext: () => void }) {
  return (
    <div className="wizard-step-body">
      <p className="wizard-welcome-greeting">{"Hi!"}</p>
      <p>
        {"Let's get your server set up -- a name, an address for clients, and an account of "}
        {"your own. Takes about a minute, and everything here stays editable later."}
      </p>
      <WizardActions>
        <button className="btn primary icon-text" type="button" onClick={onNext}>
          {"Get started"} <ArrowRight size={15} />
        </button>
      </WizardActions>
    </div>
  );
}

function IdentityStep({ onNext }: { onNext: () => void }) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [iconExt, setIconExt] = useState<string | undefined>(undefined);
  const [iconModalOpen, setIconModalOpen] = useState(false);
  const [iconBust, setIconBust] = useState(0);
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    api.serverIdentity()
      .then((info) => {
        if (cancelled) return;
        setName(info.name);
        setDescription(info.description);
        setIconExt(info.icon_ext);
        setLoaded(true);
      })
      .catch((err) => { if (!cancelled) { setError(errorMessage(err)); setLoaded(true); } });
    return () => { cancelled = true; };
  }, []);

  async function submit() {
    setBusy(true);
    setError("");
    try {
      const result = await api.action("/api/actions/set-server-identity", {
        command_id: "", reason: WIZARD_REASON, confirm: true, name, description
      });
      if (result.error) {
        setError(result.error);
        return;
      }
      onNext();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="wizard-step-body">
      <p className="wizard-step-hint">{"Shown to clients when they add this server, and in the sidebar here."}</p>
      {error && <Alert>{error}</Alert>}
      <div className="wizard-identity-row">
        <div className="avatar-edit-slot">
          {iconExt ? (
            <img className="avatar-photo-img" src={api.serverIconURL() + `&b=${iconBust}`} alt="" style={{ width: 72, height: 72 }} />
          ) : (
            <div className="avatar-fallback server-icon-fallback" style={{ width: 72, height: 72 }}>
              <ImagePlus size={22} />
            </div>
          )}
          <button className="icon-btn avatar-edit-btn" type="button" aria-label={"Add server icon"} onClick={() => setIconModalOpen(true)}>
            <ImagePlus size={13} />
          </button>
        </div>
        <div className="server-identity-fields">
          <label className="form-field"><span>{"Name"}</span><input value={name} maxLength={128} placeholder={"OwpenGram"} disabled={!loaded} onChange={(event) => setName(event.target.value)} /></label>
          <label className="form-field"><span>{"Description"}</span><textarea rows={2} value={description} maxLength={512} disabled={!loaded} onChange={(event) => setDescription(event.target.value)} /></label>
        </div>
      </div>
      <WizardActions>
        <button className="btn primary icon-text" type="button" disabled={busy || !loaded} onClick={() => void submit()}>
          {busy ? <Loader2 className="spin" size={15} /> : <ArrowRight size={15} />}
          {"Continue"}
        </button>
      </WizardActions>
      {iconModalOpen && (
        <ServerIconModal
          hasIcon={!!iconExt}
          autoReason={WIZARD_REASON}
          onClose={() => setIconModalOpen(false)}
          onDone={() => { setIconBust((n) => n + 1); void api.serverIdentity().then((info) => setIconExt(info.icon_ext)); }}
        />
      )}
    </div>
  );
}

const NETWORK_FIELDS: { key: string; label: string; hint: string; placeholder: string }[] = [
  {
    key: "TELESRV_ADVERTISE_IP",
    label: "Server public IP or hostname",
    hint: "What clients connect to. Fine to leave as 127.0.0.1 for local testing.",
    placeholder: "127.0.0.1"
  },
  {
    key: "TELESRV_PUBLIC_BASE_URL",
    label: "Public base URL",
    hint: "Used for links this server generates -- invites, sticker packs. e.g. https://example.com",
    placeholder: "http://127.0.0.1:2401"
  },
  {
    key: "TELESRV_PUBLIC_APP_SCHEME",
    label: "Custom app link scheme",
    hint: "Must match what your client builds were compiled with.",
    placeholder: "owpg"
  }
];

function NetworkStep({ onNext }: { onNext: () => void }) {
  const [values, setValues] = useState<Record<string, string>>({});
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    api.serverEnv()
      .then((groups) => {
        if (cancelled) return;
        const next: Record<string, string> = {};
        for (const group of groups) {
          for (const field of group.fields) {
            if (NETWORK_FIELDS.some((f) => f.key === field.key)) next[field.key] = field.value;
          }
        }
        setValues(next);
        setLoaded(true);
      })
      .catch((err) => { if (!cancelled) { setError(errorMessage(err)); setLoaded(true); } });
    return () => { cancelled = true; };
  }, []);

  async function submit() {
    setBusy(true);
    setError("");
    try {
      const result = await api.action("/api/actions/update-server-env", {
        command_id: "", reason: WIZARD_REASON, confirm: true, values
      });
      if (result.error) {
        setError(result.error);
        return;
      }
      onNext();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="wizard-step-body">
      <p className="wizard-step-hint">{"Takes effect once setup finishes below -- that last step restarts the server."}</p>
      {error && <Alert>{error}</Alert>}
      {NETWORK_FIELDS.map((field) => (
        <label key={field.key} className="form-field env-field">
          <span>{field.label}</span>
          <span className="env-field-desc">{field.hint}</span>
          <input
            value={values[field.key] ?? ""}
            placeholder={field.placeholder}
            disabled={!loaded}
            onChange={(event) => setValues((prev) => ({ ...prev, [field.key]: event.target.value }))}
          />
        </label>
      ))}
      <WizardActions>
        <button className="btn primary icon-text" type="button" disabled={busy || !loaded} onClick={() => void submit()}>
          {busy ? <Loader2 className="spin" size={15} /> : <ArrowRight size={15} />}
          {"Continue"}
        </button>
      </WizardActions>
    </div>
  );
}

function AccountStep({ onNext }: { onNext: () => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const incomplete = username.trim().length < 3 || password.trim() === "";

  async function submit() {
    if (incomplete) return;
    setBusy(true);
    setError("");
    try {
      const result = await api.action("/api/actions/create-admin-operator", {
        command_id: "", reason: WIZARD_REASON, confirm: true,
        username: username.trim(), password, permissions: ["*"], enabled: true
      });
      if (result.error) {
        setError(result.error);
        return;
      }
      onNext();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="wizard-step-body">
      <p className="wizard-step-hint">{"Replace the generated password with a login of your own."}</p>
      {error && <Alert>{error}</Alert>}
      <label className="form-field"><span>{"Username"}</span><input autoFocus value={username} spellCheck={false} autoCapitalize="none" placeholder={"letters, digits, dot, dash or underscore"} onChange={(event) => setUsername(event.target.value)} /></label>
      <label className="form-field"><span>{"Password"}</span><input type="password" value={password} autoComplete="new-password" onChange={(event) => setPassword(event.target.value)} /></label>
      <WizardActions>
        <button className="btn" type="button" onClick={onNext}>{"Skip for now"}</button>
        <button className="btn primary icon-text" type="button" disabled={busy || incomplete} onClick={() => void submit()}>
          {busy ? <Loader2 className="spin" size={15} /> : <UserPlus size={15} />}
          {"Create account & continue"}
        </button>
      </WizardActions>
    </div>
  );
}

// DoneStep marks setup complete and restarts, rather than leaving that for
// later -- the network fields two steps back only take effect after a
// restart, and asking the operator to remember to go find Restart in
// Services afterward is exactly the kind of loose end this wizard exists to
// close. useAdminRestartWatcher (shared with Services' own Restart button)
// reloads the page once a genuinely new process answers; passed a
// beforeReload that logs out first here specifically (Services' own Restart
// button doesn't), so the reload lands back on the login form instead of
// straight into the shell still signed in as "owpengram" -- the whole point
// of the Account step just before this one was to have a real login to
// switch to instead. The generated password stops working server-side the
// moment complete-setup runs (see identity.Store.TemporaryPasswordMatches),
// independent of this logout; this just makes sure the browser doesn't
// carry the old session forward and paper over that.
function DoneStep() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const restartWatcher = useAdminRestartWatcher();

  async function finish() {
    setBusy(true);
    setError("");
    try {
      const completeResult = await api.action("/api/actions/complete-setup", { command_id: "", reason: WIZARD_REASON, confirm: true });
      if (completeResult.error) {
        setError(completeResult.error);
        setBusy(false);
        return;
      }
      const restartResult = await api.action("/api/actions/restart-server", { command_id: "", reason: WIZARD_REASON, confirm: true });
      if (restartResult.error) {
        setError(restartResult.error);
        setBusy(false);
        return;
      }
      void restartWatcher.watch(150000, { beforeReload: async () => { await api.logout(); } });
    } catch (err) {
      setError(errorMessage(err));
      setBusy(false);
    }
  }

  return (
    <div className="wizard-step-body">
      <p>
        {"That's the essentials. Finishing restarts the server so the network settings from the previous "}
        {"step take effect. Everything here stays editable from Server Settings and Operators any time."}
      </p>
      {error && <Alert>{error}</Alert>}
      <WizardActions>
        <button className="btn primary icon-text" type="button" disabled={busy} onClick={() => void finish()}>
          {busy ? <Loader2 className="spin" size={15} /> : <Rocket size={15} />}
          {"Finish setup & restart"}
        </button>
      </WizardActions>
      {restartWatcher.waiting && (
        <RestartOverlay timedOut={false} onDismiss={restartWatcher.dismiss} />
      )}
      {restartWatcher.timedOut && (
        <RestartOverlay timedOut={true} onDismiss={restartWatcher.dismiss} />
      )}
    </div>
  );
}
