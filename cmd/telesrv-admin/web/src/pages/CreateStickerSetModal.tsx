import { Loader2, Upload, X } from "../components/icons";
import { useState } from "react";
import { createPortal } from "react-dom";
import { api, errorMessage } from "../api";
import { Alert } from "../components/ui";
// Real Telegram sticker/emoji packs are created with at least one item, so
// this form collects the title/short name plus a single starting file —
// exactly like CreateStickerSet's domain-level requirement. More stickers
// get added afterward from the pack's own preview modal.
export function CreateStickerSetModal({ kind, onClose, onCreated }: { kind: "stickers" | "emoji"; onClose: () => void; onCreated: () => void }) {
  const noun = kind === "emoji" ? "emoji" : "sticker";
  const [title, setTitle] = useState("");
  const [shortName, setShortName] = useState("");
  const [emoji, setEmoji] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit() {
    if (!title.trim() || !shortName.trim() || !emoji.trim() || !file) {
      setError(`Title, short name, emoji and a first ${noun} file are required.`);
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
      form.set("metadata", JSON.stringify({
        command_id: "", reason: reason.trim(), confirm: true,
        title: title.trim(), short_name: shortName.trim().toLowerCase(), kind, emoji: emoji.trim()
      }));
      form.set("file", file, file.name);
      await api.createStickerSet(form);
      onCreated();
      onClose();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return createPortal(
    <div className="modal-backdrop" role="presentation">
      <section className="modal command-modal" role="dialog" aria-modal="true" aria-label={`Create a new ${noun} pack`}>
        <div className="modal-head">
          <div>
            <div className="eyebrow">{"New set"}</div>
            <h2>{`Create a new ${noun} pack`}</h2>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} disabled={busy} aria-label={"Close"}><X size={15} /></button>
        </div>
        <div className="command-body">
          <div className="gift-fields-grid">
            <label><span>{"Title"}</span><input value={title} maxLength={64} onChange={(event) => setTitle(event.target.value)} /></label>
            <label><span>{"Short name"}</span><input value={shortName} maxLength={32} onChange={(event) => setShortName(event.target.value)} placeholder={"lowercase_short_name"} /></label>
            <label><span>{"Emoji"}</span><input value={emoji} onChange={(event) => setEmoji(event.target.value)} placeholder={"e.g. 😀"} /></label>
          </div>
          <label className={`gift-file-picker ${file ? "has-file" : ""}`}>
            <input type="file" accept=".tgs,.json,.webp,application/json,application/x-tgsticker,image/webp" onChange={(event) => setFile(event.target.files?.[0] ?? null)} />
            <span className="gift-file-copy"><span className="gift-field-label">{`First ${noun}`}</span><strong>{file ? file.name : "Choose a TGS, Lottie JSON, or WebP file"}</strong></span>
            <span className="gift-file-action">{file ? "Change file" : "Choose file"}</span>
          </label>
          <label className="gift-reason-field"><span>{"Audit reason"}</span><input value={reason} placeholder={"Briefly describe why this gift is being imported"} onChange={(event) => setReason(event.target.value)} /></label>
          {error && <Alert>{error}</Alert>}
        </div>
        <div className="modal-actions">
          <button className="btn" type="button" onClick={onClose} disabled={busy}>{"Close"}</button>
          <button className="btn primary" type="button" onClick={submit} disabled={busy}>
            {busy ? <Loader2 className="spin" size={15} /> : <Upload size={15} />}
            {`Create ${noun} pack`}
          </button>
        </div>
      </section>
    </div>,
    document.body
  );
}
