import { ArrowLeft, Loader2 } from "lucide-react";
import type { FormEvent } from "react";
import { useEffect, useRef, useState } from "react";
import { api, errorMessage } from "../api";
import { AppBackground } from "../components/AppBackground";
import { Alert } from "../components/ui";
import { ThemeSwitch } from "../theme";
import type { AdminSession, PublicBranding } from "../types";

export function LoginPage({ onLogin }: { onLogin: (session: AdminSession) => void }) {
  const [username, setUsername] = useState("");
  const [secret, setSecret] = useState("");
  // Which field is showing. The password field mounts only on step 1, so
  // there is never a submit handler wired to a "confirm" or "log in" button
  // that could fire with a still-empty password field -- credentials only
  // ever reach api.login once both are on screen and this has advanced.
  const [step, setStep] = useState<0 | 1>(0);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const usernameRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);
  // Whose server this is. Fetched without a session -- the name and icon are
  // already public from owpengram-server's client endpoints -- and left null on
  // failure so the panel simply keeps its own branding.
  const [branding, setBranding] = useState<PublicBranding | null>(null);
  const [iconFailed, setIconFailed] = useState(false);

  useEffect(() => {
    api.publicBranding().then(setBranding).catch(() => undefined);
  }, []);

  useEffect(() => {
    // preventScroll matters here: .login-wizard clips with overflow:hidden and
    // is itself a scroll container, so a plain .focus() makes the browser
    // scroll it to reveal the field -- fighting the translateX slide and
    // leaving a stray scrollLeft behind that then desyncs every later step
    // change from what the transform shows.
    if (step === 1) {
      passwordRef.current?.focus({ preventScroll: true });
    } else {
      usernameRef.current?.focus({ preventScroll: true });
    }
  }, [step]);

  const serverName = branding?.name?.trim() || "OwpenGram";
  const iconSrc = branding?.has_icon && !iconFailed ? api.publicIconURL() : "/logo.png";

  function goToPassword() {
    if (!username.trim()) return;
    setError("");
    setStep(1);
  }

  function goToUsername() {
    setError("");
    setStep(0);
  }

  // The form has one submit handler regardless of step, because Enter inside
  // any of its text inputs fires it -- routing that here means the username
  // field's Enter key advances instead of submitting a login with no password.
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (step === 0) {
      goToPassword();
      return;
    }
    setBusy(true);
    setError("");
    try {
      // api.login remembers the CSRF token and tells us the sign-in worked.
      const result = await api.login(secret, username);
      // The session itself is then read from /api/session rather than assembled
      // out of the login answer. The login response carries only the actor and
      // the permissions, so building a session from it silently dropped the
      // build info, the API layers and the third-party-verification flag --
      // which is why the sidebar footer was blank until the page was reloaded.
      // One endpoint decides what a session is.
      try {
        onLogin(await api.session());
      } catch {
        // Signed in, but the follow-up read failed. Falling back to what the
        // login answer does carry beats bouncing someone back to a login form
        // they have already passed; a reload fills in the rest.
        onLogin({ actor: result.actor, permissions: result.permissions ?? [] });
      }
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="login-page">
      <AppBackground />
      <section className="login-panel">
        <div className="login-head">
          <div className="brand brand-elevated">
            <span className="brand-mark">
              <img src={iconSrc} alt={serverName} onError={() => setIconFailed(true)} />
            </span>
            <span>
              <strong>{serverName}</strong>
              <small>{"Admin Console"}</small>
            </span>
          </div>
          <div className="login-head-actions">
            <ThemeSwitch />
          </div>
        </div>
        <div className="login-copy">
          <h1>{"Operations Admin"}</h1>
          <p>{"Enter credentials to open the console."}</p>
        </div>
        {error && <Alert>{error}</Alert>}
        <form className="form-stack" onSubmit={submit}>
          <div className="login-wizard">
            <div className="login-wizard-track" style={{ transform: `translateX(-${step * 100}%)` }}>
              <div className="login-wizard-step" aria-hidden={step !== 0}>
                <label>
                  <span>{"Username"}</span>
                  <input
                    ref={usernameRef}
                    type="text"
                    value={username}
                    autoComplete="username"
                    spellCheck={false}
                    autoCapitalize="none"
                    placeholder={"login"}
                    tabIndex={step === 0 ? undefined : -1}
                    onChange={(event) => setUsername(event.target.value)}
                  />
                </label>
              </div>
              <div className="login-wizard-step" aria-hidden={step !== 1}>
                <label>
                  <span>{"Password"}</span>
                  <input
                    ref={passwordRef}
                    type="password"
                    value={secret}
                    autoComplete="current-password"
                    placeholder={"password"}
                    tabIndex={step === 1 ? undefined : -1}
                    onChange={(event) => setSecret(event.target.value)}
                  />
                </label>
              </div>
            </div>
          </div>
          {step === 0 ? (
            <button className="btn primary full" type="submit" disabled={!username.trim()}>
              {"Next"}
            </button>
          ) : (
            <div className="login-wizard-actions">
              <button className="btn icon-text" type="button" onClick={goToUsername}>
                <ArrowLeft size={15} />
                {"Back"}
              </button>
              <button className="btn primary" type="submit" disabled={busy}>
                {busy ? <Loader2 className="spin" size={15} /> : null}
                {busy ? "Logging in" : "Log in"}
              </button>
            </div>
          )}
        </form>
      </section>
    </main>
  );
}
