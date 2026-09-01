import { Send, X } from "lucide-react";
import { useMemo, useState } from "react";
import { createPortal } from "react-dom";
import type { AccountRow } from "../types";
import { ActionButton } from "./ActionButton";
import { MultiUserPicker } from "./EntityPicker";

type TargetMode = "all" | "selected";

// CreateBroadcastModal composes the message and target list, then hands off to
// ActionButton for the usual dry-run/confirm flow. "All users" is never
// resolved into an id list at all -- the admin service snapshots the
// current eligible user set itself and a background worker enumerates it
// incrementally, so this only ever sends user_ids for "selected" mode,
// where the picker deals with an actual, visible list of accounts.
export function CreateBroadcastModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [message, setMessage] = useState("");
  const [targetMode, setTargetMode] = useState<TargetMode>("all");
  const [recipients, setRecipients] = useState<AccountRow[]>([]);

  const disabled = useMemo(() => {
    if (!message.trim()) return true;
    if (targetMode === "selected" && recipients.length === 0) return true;
    return false;
  }, [message, targetMode, recipients]);

  return createPortal(
    <div className="modal-backdrop" role="presentation">
      <section className="modal command-modal" role="dialog" aria-modal="true" aria-label={"Send broadcast"}>
        <div className="modal-head">
          <div>
            <div className="eyebrow">{"Broadcasts"}</div>
            <h2>{"Send broadcast"}</h2>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} aria-label={"Close"}><X size={15} /></button>
        </div>
        <div className="command-body">
          <p>{"Sends a message from the official system account (777000) to all users or to a chosen list. Delivery happens in the background and may take a few minutes for large audiences."}</p>
          <label className="form-field">
            <span>{"Message"}</span>
            <textarea
              value={message}
              onChange={(event) => setMessage(event.target.value)}
              rows={5}
              maxLength={4096}
              placeholder={"What's new..."}
            />
          </label>
          <div className="bot-create-fields">
            <label className="duration-field">
              <span>{"Target"}</span>
              <select value={targetMode} onChange={(event) => setTargetMode(event.target.value as TargetMode)}>
                <option value="all">{"All users"}</option>
                <option value="selected">{"Selected users"}</option>
              </select>
            </label>
          </div>
          {targetMode === "selected" && (
            <MultiUserPicker label={"Recipients"} selected={recipients} onChange={setRecipients} />
          )}
        </div>
        <div className="modal-actions">
          <button className="btn" type="button" onClick={onClose}>{"Close"}</button>
          <ActionButton
            label={"Send broadcast"}
            icon={<Send size={15} />}
            tone="neutral"
            path="/api/actions/create-broadcast"
            disabled={disabled}
            payload={() => ({
              message: message.trim(),
              target_mode: targetMode,
              user_ids: targetMode === "selected" ? recipients.map((row) => row.ID) : undefined
            })}
            onDone={onCreated}
          />
        </div>
      </section>
    </div>,
    document.body
  );
}
