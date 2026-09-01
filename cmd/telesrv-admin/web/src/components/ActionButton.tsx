import { Check, CheckCircle2, CircleAlert, Copy, FileJson, Loader2, Play, X } from "lucide-react";
import type { ReactNode } from "react";
import { useMemo, useState } from "react";
import { createPortal } from "react-dom";
import { api, errorMessage } from "../api";
import { copyToClipboard } from "../clipboard";
import type { CommandResult } from "../types";
import { Alert, JsonBlock } from "./ui";

type ActionTone = "neutral" | "warn" | "danger" | "primary";

export function ActionButton({
  label,
  path,
  payload,
  icon,
  compact = false,
  tone = "danger",
  disabled = false,
  onDone,
  onError,
  secretField
}: {
  label: string;
  path: string;
  payload: () => Record<string, unknown>;
  icon?: ReactNode;
  compact?: boolean;
  tone?: ActionTone;
  // disabled keeps a form from opening the confirm flow at all while its own
  // validation is unhappy, so the operator fixes the field instead of reading a
  // backend rejection.
  disabled?: boolean;
  onDone?: () => void;
  // onError lets a page react to a failure the operator cannot fix by editing the
  // form — an optimistic-locking 409, say — and replace the raw backend text with
  // an explanation by returning it.
  onError?: (error: unknown) => string | undefined;
  // secretField names a key in result.details that holds a one-time secret
  // (e.g. a bot token) -- when present, it's pulled out of the generic JSON
  // dump and rendered instead as its own copy-to-clipboard callout, so it
  // doesn't get lost among the other fields.
  secretField?: string;
}) {
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("");
  const [result, setResult] = useState<CommandResult | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [secretCopied, setSecretCopied] = useState(false);

  function reset() {
    setReason("");
    setResult(null);
    setError("");
    setSecretCopied(false);
  }

  async function run(confirm: boolean) {
    if (!reason.trim()) {
      setError("Please enter an operation reason");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const body = { ...payload(), reason, confirm };
      const commandResult = await api.action(path, body);
      setResult(commandResult);
      if (confirm) {
        onDone?.();
      }
    } catch (err) {
      setError(onError?.(err) || errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const canConfirm = result?.dry_run && !result.error;
  const triggerClass = `btn ${tone === "danger" ? "danger" : tone === "warn" ? "warn" : tone === "primary" ? "primary" : ""} ${compact ? "compact-btn" : ""}`;
  const previewPayload = useMemo(() => {
    try {
      return payload();
    } catch (err) {
      return { payload_error: errorMessage(err) };
    }
  }, [open, payload]);

  const secretValue = secretField && result?.details && typeof result.details[secretField] === "string"
    ? (result.details[secretField] as string)
    : "";
  const visibleDetails = secretValue && result?.details
    ? Object.fromEntries(Object.entries(result.details).filter(([key]) => key !== secretField))
    : result?.details;

  async function copySecret() {
    await copyToClipboard(secretValue);
    setSecretCopied(true);
  }

  return (
    <>
      <button
        className={triggerClass}
        type="button"
        disabled={disabled}
        onClick={() => {
          reset();
          setOpen(true);
        }}
      >
        {icon}
        {label}
      </button>
      {open && createPortal(
        <div className="modal-backdrop" role="presentation">
          <section className="modal command-modal" role="dialog" aria-modal="true" aria-label={label}>
            <div className="modal-head">
              <div>
                <div className="eyebrow">{"Action Flow"}</div>
                <h2>{label}</h2>
              </div>
              <button className="icon-btn" type="button" onClick={() => setOpen(false)} aria-label={"Close"}><X size={15} /></button>
            </div>
            <div className="command-body">
              <div className="command-steps">
                <div className={`command-step ${reason.trim() ? "done" : "active"}`}>
                  <span>1</span><strong>{"Enter reason"}</strong>
                </div>
                <div className={`command-step ${result?.dry_run ? "done" : reason.trim() ? "active" : ""}`}>
                  <span>2</span><strong>{"Dry-run check"}</strong>
                </div>
                <div className={`command-step ${result && !result.dry_run && !result.error ? "done" : canConfirm ? "active" : ""}`}>
                  <span>3</span><strong>{"Confirm execution"}</strong>
                </div>
              </div>
              <label className="form-field">
                <span>{"Operation reason"}</span>
                <textarea value={reason} onChange={(event) => setReason(event.target.value)} rows={3} placeholder={"Describe why this operation is being performed"} />
              </label>
              <div className="command-preview">
                <div className="preview-head"><FileJson size={14} /> {"Request preview"}</div>
                <JsonBlock value={JSON.stringify(previewPayload, null, 2)} />
              </div>
              {error && <Alert>{error}</Alert>}
              {result && (
                <div className="result-box">
                <div className="result-title">
                    {result.error ? <CircleAlert size={16} /> : <CheckCircle2 size={16} />}
                  <strong>{result.message || result.error || "Action result"}</strong>
                </div>
                <div className="result-line"><span>{"Command ID"}</span><strong>{result.command_id}</strong></div>
                <div className="result-line"><span>{"Status"}</span><strong>{result.status}</strong></div>
                <div className="result-line"><span>{"Dry-run"}</span><strong>{result.dry_run ? "Yes" : "No"}</strong></div>
                  <div className="result-message">{result.message || result.error}</div>
                  {secretValue && (
                    <div className="secret-reveal">
                      <div className="secret-reveal-label">{"One-time secret — copy it now, it won't be shown again"}</div>
                      <div className="secret-reveal-row">
                        <code className="secret-reveal-value">{"•".repeat(Math.min(secretValue.length, 40))}</code>
                        <button className="btn icon-text" type="button" onClick={() => void copySecret()}>
                          {secretCopied ? <Check size={15} /> : <Copy size={15} />}
                          {secretCopied ? "Copied" : "Copy"}
                        </button>
                      </div>
                    </div>
                  )}
                  {visibleDetails && Object.keys(visibleDetails).length > 0 && <JsonBlock value={JSON.stringify(visibleDetails, null, 2)} />}
                </div>
              )}
            </div>
            <div className="modal-actions">
              <button className="btn" type="button" onClick={() => setOpen(false)}>{"Close"}</button>
              <button className="btn icon-text" type="button" onClick={() => run(false)} disabled={busy}>
                {busy ? <Loader2 size={15} className="spin" /> : <Play size={15} />}
                {result ? "Run dry-run again" : "Run dry-run first"}
              </button>
              <button className="btn danger icon-text" type="button" onClick={() => run(true)} disabled={busy || !canConfirm}>
                <CheckCircle2 size={15} />
                {"Confirm execution"}
              </button>
            </div>
          </section>
        </div>,
        document.body
      )}
    </>
  );
}
