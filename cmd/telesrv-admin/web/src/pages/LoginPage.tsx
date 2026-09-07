import type { FormEvent } from "react";
import { useState } from "react";
import { api, errorMessage } from "../api";
import { Alert } from "../components/ui";
import { ThemeSwitch } from "../theme";
import type { AdminSession } from "../types";

export function LoginPage({ onLogin }: { onLogin: (session: AdminSession) => void }) {
  // Deliberately not pre-filled: the built-in operator is a default name, not a
  // default identity, and typing it is the difference between choosing to use
  // it and drifting into it. The server rejects a blank username either way.
  const [username, setUsername] = useState("");
  const [secret, setSecret] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      // The login answer carries the permission set and the CSRF token; api.login
      // remembers the token, the session state keeps the rights.
      const result = await api.login(secret, username);
      onLogin({ actor: result.actor, permissions: result.permissions ?? [] });
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="login-page">
      <div className="bg-orbs" aria-hidden="true">
        <div className="bg-orb bg-orb--1" />
        <div className="bg-orb bg-orb--2" />
        <div className="bg-orb bg-orb--3" />
      </div>
      <section className="login-panel">
        <div className="login-head">
          <div className="brand brand-elevated">
            <span className="brand-mark"><img src="/logo.png" alt="OwpenGram" /></span>
            <span>
              <strong>OwpenGram</strong>
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
          <label>
            <span>{"Username"}</span>
            <input
              autoFocus
              type="text"
              value={username}
              autoComplete="username"
              spellCheck={false}
              autoCapitalize="none"
              // A hint, not a value: it names the built-in operator without
              // filling the field in, so signing in as it stays a deliberate act.
              placeholder={"owpengram"}
              onChange={(event) => setUsername(event.target.value)}
            />
          </label>
          <label>
            <span>{"Password"}</span>
            <input
              type="password"
              value={secret}
              autoComplete="current-password"
              onChange={(event) => setSecret(event.target.value)}
            />
          </label>
          <button className="btn primary full" type="submit" disabled={busy}>
            {busy ? "Logging in" : "Log in"}
          </button>
        </form>
      </section>
    </main>
  );
}
