import { Lock, ShieldKeyhole } from "@gravity-ui/icons";
import { createContext, useContext, useState, type FormEvent, type ReactNode } from "react";
import { errorMessage } from "../api";
import { isViewLocked, privateView, type ViewKind } from "../lib/privateView";

// PrivateGate stands in front of everything private about one account or
// channel. The operator types their own console password again; the server
// checks it, writes the opening to the audit log and returns a view grant.
// The grant is kept in this component's state only: leaving the page, or
// reloading it, asks for the password again.

type Grant = { grant: string; relock: () => void; onError: (err: unknown) => boolean };

const GrantContext = createContext<Grant | null>(null);

export function useViewGrant(): Grant {
  const value = useContext(GrantContext);
  if (!value) throw new Error("useViewGrant outside PrivateGate");
  return value;
}

export function PrivateGate({ kind, id, what, children }: { kind: ViewKind; id: number; what: string; children: ReactNode }) {
  const [grant, setGrant] = useState("");
  const [password, setPassword] = useState("");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [expired, setExpired] = useState(false);

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!password || busy) return;
    setBusy(true);
    setError("");
    try {
      const result = await privateView.unlock(kind, id, password, reason.trim());
      setGrant(result.grant);
      setExpired(false);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setPassword("");
      setBusy(false);
    }
  }

  if (grant) {
    const value: Grant = {
      grant,
      relock: () => setGrant(""),
      // A child that hits an expired grant hands the error here; the page
      // drops back to the password prompt instead of showing an error.
      onError: (err) => {
        if (!isViewLocked(err)) return false;
        setGrant("");
        setExpired(true);
        return true;
      }
    };
    return <GrantContext.Provider value={value}>{children}</GrantContext.Provider>;
  }

  return (
    <section className="private-gate">
      <div className="private-gate-icon" aria-hidden="true">
        <ShieldKeyhole width={26} height={26} />
      </div>
      <h2>{expired ? "Access expired" : "Private content"}</h2>
      <p>
        {`${what} are private. Enter your console password to open them. `}
        <strong>{"Every opening is written to the audit log under your name."}</strong>
      </p>
      <form className="private-gate-form" onSubmit={(event) => void submit(event)} autoComplete="off">
        <input
          type="password"
          name="view-password"
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          placeholder={"Your console password"}
          aria-label={"Your console password"}
          autoComplete="off"
          data-1p-ignore
          data-lpignore="true"
          data-bwignore
          autoFocus
        />
        <input
          type="text"
          name="view-reason"
          value={reason}
          onChange={(event) => setReason(event.target.value)}
          placeholder={"Reason (optional, goes to the audit log)"}
          aria-label={"Reason"}
          autoComplete="off"
          maxLength={500}
        />
        {error && <div className="private-gate-error">{error}</div>}
        <button className="btn primary icon-text" type="submit" disabled={!password || busy}>
          <Lock /> {busy ? "Checking…" : "Unlock"}
        </button>
      </form>
    </section>
  );
}
