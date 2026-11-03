import { Plus, X } from "./icons";
import { useState } from "react";
import { createPortal } from "react-dom";
import { toInt } from "../lib/format";
import { ActionButton } from "./ActionButton";

// CreateBotModal collects the new bot's fields, then hands off to ActionButton
// for the usual reason/dry-run/confirm flow -- the token is shown once in
// ActionButton's own result panel after confirmation.
export function CreateBotModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [ownerID, setOwnerID] = useState("");
  const [botName, setBotName] = useState("");
  const [botUsername, setBotUsername] = useState("");

  return createPortal(
    <div className="modal-backdrop" role="presentation">
      <section className="modal command-modal" role="dialog" aria-modal="true" aria-label={"Create bot"}>
        <div className="modal-head">
          <div>
            <div className="eyebrow">{"Bots"}</div>
            <h2>{"Create bot"}</h2>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} aria-label={"Close"}><X size={15} /></button>
        </div>
        <div className="command-body">
          <p>{"Provision a bot account owned by the given user. The token is shown once after confirmation."}</p>
          <div className="bot-create-fields">
            <label className="duration-field">
              <span>{"Owner user ID"}</span>
              <input
                value={ownerID}
                onChange={(event) => setOwnerID(event.target.value)}
                type="number"
                min="1"
                placeholder="123456789"
              />
            </label>
            <label className="duration-field">
              <span>{"Display name"}</span>
              <input value={botName} onChange={(event) => setBotName(event.target.value)} placeholder={"e.g. Service Bot"} maxLength={64} />
            </label>
            <label className="duration-field">
              <span>{"Username"}</span>
              <input value={botUsername} onChange={(event) => setBotUsername(event.target.value)} placeholder="my_service_bot" />
            </label>
          </div>
          <span className="bot-create-note">{"Username must be 5-32 characters and end with 'bot'."}</span>
        </div>
        <div className="modal-actions">
          <button className="btn" type="button" onClick={onClose}>{"Close"}</button>
          <ActionButton
            label={"Create bot"}
            icon={<Plus size={15} />}
            tone="neutral"
            path="/api/actions/create-bot"
            payload={() => ({
              owner_user_id: toInt(ownerID),
              name: botName.trim(),
              username: botUsername.trim().replace(/^@/, "")
            })}
            secretField="token"
            onDone={onCreated}
          />
        </div>
      </section>
    </div>,
    document.body
  );
}
