import { CircleExclamation, ScalesBalanced, Lock, LockOpen, Person, Shield, ShieldKeyhole, SquareListUl } from "@gravity-ui/icons";
import { createContext, useContext, useEffect, useState, type FormEvent, type ReactNode } from "react";
import { errorMessage } from "../api";
import { isViewLocked, privateView, type ViewKind } from "../lib/privateView";

// PrivateGate stands in front of everything private about one account or
// channel. Opening it takes a stated purpose, a written reason, an explicit
// confirmation and the operator's own console password. The server checks all
// of it, writes the opening to the audit log (who, why, from where) and
// returns a short-lived view grant; every chat read afterwards is logged with
// the same reason. The grant lives only in this component: leaving the page,
// reloading it, pressing "Lock now" or the timer running out locks it again.

type Grant = { grant: string; relock: () => void; onError: (err: unknown) => boolean };

const GrantContext = createContext<Grant | null>(null);

export function useViewGrant(): Grant {
  const value = useContext(GrantContext);
  if (!value) throw new Error("useViewGrant outside PrivateGate");
  return value;
}

// Kept in step with viewPurposes in cmd/telesrv-admin/privateview.go.
const PURPOSES: { key: string; label: string; icon: ReactNode }[] = [
  { key: "report", label: "Investigating a report", icon: <SquareListUl /> },
  { key: "security", label: "Security incident or abuse", icon: <Shield /> },
  { key: "legal", label: "Legal or law-enforcement request", icon: <ScalesBalanced /> },
  { key: "owner", label: "Request from the account owner", icon: <Person /> },
  { key: "other", label: "Other", icon: <CircleExclamation /> }
];
const REASON_MIN = 15;
const REASON_MAX = 500;

function remaining(until: number): string {
  const s = Math.max(0, Math.round((until - Date.now()) / 1000));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

export function PrivateGate({ kind, id, what, children }: { kind: ViewKind; id: number; what: string; children: ReactNode }) {
  const [grant, setGrant] = useState("");
  const [until, setUntil] = useState(0);
  const [grantedFor, setGrantedFor] = useState("");
  const [, tick] = useState(0);
  const [purpose, setPurpose] = useState("");
  const [reason, setReason] = useState("");
  const [ack, setAck] = useState(false);
  const [ackConsequences, setAckConsequences] = useState(false);
  const [ackNotified, setAckNotified] = useState(false);
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [ended, setEnded] = useState<"" | "expired" | "locked">("");

  const reasonLength = reason.trim().replace(/\s+/g, " ").length;
  const ready = Boolean(purpose) && reasonLength >= REASON_MIN && reasonLength <= REASON_MAX && ack && ackConsequences && ackNotified && Boolean(password) && !busy;

  function lock(why: "expired" | "locked") {
    setGrant("");
    setUntil(0);
    setEnded(why);
  }

  // The timer both shows the time left and closes the view when it runs out,
  // instead of waiting for the next request to be refused.
  useEffect(() => {
    if (!grant) return;
    const timer = window.setInterval(() => {
      if (Date.now() >= until) lock("expired");
      else tick((n) => n + 1);
    }, 1000);
    return () => window.clearInterval(timer);
  }, [grant, until]);

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!ready) return;
    setBusy(true);
    setError("");
    try {
      const result = await privateView.unlock(kind, id, {
        password,
        purpose,
        reason: reason.trim(),
        acknowledge: ack,
        acknowledge_consequences: ackConsequences,
        acknowledge_notified: ackNotified
      });
      setGrant(result.grant);
      setUntil(new Date(result.expires_at).getTime());
      setGrantedFor(result.reason);
      setEnded("");
      // A fresh justification is needed next time.
      setReason("");
      setAck(false);
      setAckConsequences(false);
      setAckNotified(false);
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
      relock: () => lock("locked"),
      // A child that hits an expired grant hands the error here; the page
      // drops back to the prompt instead of showing an error.
      onError: (err) => {
        if (!isViewLocked(err)) return false;
        lock("expired");
        return true;
      }
    };
    return (
      <GrantContext.Provider value={value}>
        <div className="private-session" role="status">
          <LockOpen className="private-session-icon" />
          <span className="private-session-text">
            <strong>{"Private content unlocked"}</strong>
            <span title={grantedFor}>{grantedFor}</span>
          </span>
          <span className="private-session-timer" title={"Locks automatically when the time runs out"}>{remaining(until)}</span>
          <button className="btn icon-text" type="button" onClick={() => lock("locked")}>
            <Lock /> {"Lock now"}
          </button>
        </div>
        {children}
      </GrantContext.Provider>
    );
  }

  return (
    <section className="private-gate">
      <header className="private-gate-head">
        <span className="private-gate-icon" aria-hidden="true">
          <ShieldKeyhole width={24} height={24} />
        </span>
        <div>
          <h2>{ended === "expired" ? "Access expired" : ended === "locked" ? "Locked" : "Private content"}</h2>
          <p>{`${what} are private.`}</p>
        </div>
      </header>

      <div className="private-gate-warning">
        <CircleExclamation />
        <span>
          {"Reading someone's private messages needs a real reason. "}
          <strong>{"Your name, the reason, your IP address and every chat you open are written to the audit log"}</strong>
          {" and visible to other administrators. "}
          <strong>{kind === "user" ? "The person is notified" : "The owner is notified"}</strong>
          {" by the system account, with your reason."}
        </span>
      </div>

      <form className="private-gate-form" onSubmit={(event) => void submit(event)} autoComplete="off">
        <fieldset className="private-gate-purposes">
          <legend>{"Why do you need access?"}</legend>
          {PURPOSES.map((p) => (
            <label key={p.key} className={`purpose-option ${purpose === p.key ? "active" : ""}`}>
              <input type="radio" name="view-purpose" value={p.key} checked={purpose === p.key} onChange={() => setPurpose(p.key)} />
              {p.icon}
              <span>{p.label}</span>
            </label>
          ))}
        </fieldset>

        <label className="private-gate-field">
          <span>
            {"Explain the reason"}
            <em className={reasonLength >= REASON_MIN ? "ok" : ""}>{`${reasonLength} / ${REASON_MIN}+`}</em>
          </span>
          <textarea
            name="view-reason"
            value={reason}
            onChange={(event) => setReason(event.target.value)}
            placeholder={"e.g. Case #42: checking whether the reported messages are spam"}
            rows={3}
            maxLength={REASON_MAX}
            autoComplete="off"
          />
        </label>

        <label className="private-gate-field">
          <span>{"Your console password"}</span>
          <input
            type="password"
            name="view-password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            autoComplete="off"
            data-1p-ignore
            data-lpignore="true"
            data-bwignore
          />
        </label>

        <div className="private-gate-acks">
          <label className="private-gate-ack">
            <input type="checkbox" checked={ack} onChange={(event) => setAck(event.target.checked)} />
            <span>{"I have a legitimate reason to see this private content and will use it only for that."}</span>
          </label>
          <label className="private-gate-ack">
            <input type="checkbox" checked={ackConsequences} onChange={(event) => setAckConsequences(event.target.checked)} />
            <span>{"I understand the consequences of my actions."}</span>
          </label>
          <label className="private-gate-ack">
            <input type="checkbox" checked={ackNotified} onChange={(event) => setAckNotified(event.target.checked)} />
            <span>
              {kind === "user"
                ? "I understand that this person will be notified of my actions, with the reason I gave."
                : "I understand that the owner will be notified of my actions, with the reason I gave."}
            </span>
          </label>
        </div>

        {error && <div className="private-gate-error">{error}</div>}

        <button className="btn primary icon-text private-gate-submit" type="submit" disabled={!ready}>
          <Lock /> {busy ? "Checking…" : "Unlock for 10 minutes"}
        </button>
        <p className="private-gate-foot">{"Access ends after 10 minutes, when you leave this page or when you lock it."}</p>
      </form>
    </section>
  );
}
