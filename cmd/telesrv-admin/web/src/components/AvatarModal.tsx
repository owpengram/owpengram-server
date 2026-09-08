import { ImagePlus, Loader2, Upload, X } from "lucide-react";
import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { api, errorMessage } from "../api";
import { Alert } from "./ui";

type AvatarModalKind = "user" | "channel";

// AvatarModal uploads a new photo for an account or a channel/supergroup. It
// follows CreateStickerSetModal's shape (a single reason field + direct
// execute, rather than ActionButton's JSON dry-run/confirm flow) since the
// payload is a multipart file upload, not a plain JSON body.
export function AvatarModal({ kind, id, onClose, onDone }: { kind: AvatarModalKind; id: number; onClose: () => void; onDone: () => void }) {
  const [file, setFile] = useState<File | null>(null);
  const [previewURL, setPreviewURL] = useState("");
  const [videoStartTs, setVideoStartTs] = useState("0");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const isVideo = kind === "user" && !!file && file.type.startsWith("video/");

  useEffect(() => {
    if (!file) {
      setPreviewURL("");
      return;
    }
    const url = URL.createObjectURL(file);
    setPreviewURL(url);
    return () => URL.revokeObjectURL(url);
  }, [file]);

  async function submit() {
    if (!file) {
      setError("Choose an image or video file first.");
      return;
    }
    if (!reason.trim()) {
      setError("Please enter an operation reason");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const idField = kind === "channel" ? "channel_id" : "user_id";
      const form = new FormData();
      const metadata: Record<string, unknown> = { command_id: "", reason: reason.trim(), confirm: true, [idField]: id };
      if (isVideo) {
        metadata.video_start_ts = Number(videoStartTs) || 0;
      }
      form.set("metadata", JSON.stringify(metadata));
      form.set("file", file, file.name);
      const result = kind === "channel" ? await api.setChannelAvatar(form) : isVideo ? await api.setAccountAvatarVideo(form) : await api.setAccountAvatar(form);
      if (result.error) {
        setError(result.error);
        return;
      }
      onDone();
      onClose();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const noun = kind === "channel" ? "Channel" : "Account";

  return createPortal(
    <div className="modal-backdrop" role="presentation">
      <section className="modal command-modal" role="dialog" aria-modal="true" aria-label={"Change avatar"}>
        <div className="modal-head">
          <div>
            <div className="eyebrow">{noun}</div>
            <h2>{"Change avatar"}</h2>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} disabled={busy} aria-label={"Close"}><X size={15} /></button>
        </div>
        <div className="command-body">
          <label className={`gift-file-picker ${file ? "has-file" : ""}`}>
            <input
              type="file"
              accept={kind === "user" ? "image/png,image/jpeg,image/webp,video/mp4" : "image/png,image/jpeg,image/webp"}
              onChange={(event) => setFile(event.target.files?.[0] ?? null)}
            />
            {previewURL ? (
              isVideo ? (
                <video className="gift-file-icon" src={previewURL} style={{ objectFit: "cover" }} muted loop autoPlay />
              ) : (
                <img className="gift-file-icon" src={previewURL} alt="" style={{ objectFit: "cover" }} />
              )
            ) : (
              <ImagePlus size={22} />
            )}
            <span className="gift-file-copy">
              <span className="gift-field-label">{"New avatar"}</span>
              <strong>{file ? file.name : kind === "user" ? "Choose a JPEG, PNG, WebP image, or MP4 video" : "Choose a JPEG, PNG, or WebP image"}</strong>
            </span>
            <span className="gift-file-action">{file ? "Change file" : "Choose file"}</span>
          </label>
          {isVideo && (
            <label className="gift-reason-field">
              <span>{"Video start (seconds)"}</span>
              <input type="number" min="0" step="0.1" value={videoStartTs} onChange={(event) => setVideoStartTs(event.target.value)} />
            </label>
          )}
          <label className="gift-reason-field"><span>{"Audit reason"}</span><input value={reason} placeholder={"Briefly describe why this avatar is being changed"} onChange={(event) => setReason(event.target.value)} /></label>
          {error && <Alert>{error}</Alert>}
        </div>
        <div className="modal-actions">
          <button className="btn" type="button" onClick={onClose} disabled={busy}>{"Close"}</button>
          <button className="btn primary" type="button" onClick={submit} disabled={busy}>
            {busy ? <Loader2 className="spin" size={15} /> : <Upload size={15} />}
            {"Upload avatar"}
          </button>
        </div>
      </section>
    </div>,
    document.body
  );
}
