import { Check, Copy, X } from "lucide-react";
import { useState } from "react";
import { createPortal } from "react-dom";
import { api, errorMessage } from "../api";
import { copyToClipboard } from "../clipboard";
import { Alert } from "./ui";

// CopyBotTokenModal writes a non-system bot's token straight to the
// clipboard without ever rendering it on screen -- the value only ever
// lives in the fetch response and the clipboard API call; React state only
// ever tracks whether the copy succeeded, never the token itself.
export function CopyBotTokenModal({ botID, onClose }: { botID: number; onClose: () => void }) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);

  async function copyToken() {
    if (!reason.trim()) {
      setError("Please enter an operation reason");
      return;
    }
    setBusy(true);
    setError("");
    setCopied(false);
    try {
      const result = await api.action("/api/actions/export-bot-token", {
        command_id: "",
        reason: reason.trim(),
        confirm: true,
        bot_user_id: botID
      });
      const token = result.details?.token;
      if (result.error || typeof token !== "string" || !token) {
        setError(result.error || "No token returned.");
        return;
      }
      await copyToClipboard(token);
      setCopied(true);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return createPortal(
    <div className="modal-backdrop" role="presentation">
      <section className="modal command-modal" role="dialog" aria-modal="true" aria-label={"Copy bot token"}>
        <div className="modal-head">
          <div>
            <div className="eyebrow">{"Bot"}</div>
            <h2>{"Copy bot token"}</h2>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} disabled={busy} aria-label={"Close"}><X size={15} /></button>
        </div>
        <div className="command-body">
          <p>{"The token is written straight to your clipboard and is never shown on screen. Paste it wherever it's needed right after copying."}</p>
          <label className="form-field">
            <span>{"Operation reason"}</span>
            <textarea value={reason} onChange={(event) => setReason(event.target.value)} rows={3} placeholder={"Describe why this token is being retrieved"} />
          </label>
          {error && <Alert>{error}</Alert>}
          {copied && (
            <div className="secret-reveal">
              <div className="secret-reveal-label"><Check size={14} /> {"Token copied to clipboard."}</div>
            </div>
          )}
        </div>
        <div className="modal-actions">
          <button className="btn" type="button" onClick={onClose} disabled={busy}>{"Close"}</button>
          <button className="btn primary icon-text" type="button" onClick={() => void copyToken()} disabled={busy}>
            <Copy size={15} /> {copied ? "Copy again" : "Copy token"}
          </button>
        </div>
      </section>
    </div>,
    document.body
  );
}
