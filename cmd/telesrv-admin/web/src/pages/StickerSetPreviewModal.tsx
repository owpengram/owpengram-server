import { ChevronLeft, ChevronRight, Loader2, Plus, Trash2, X } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { StickerDocumentPreview } from "../components/StickerDocumentPreview";
import { Alert } from "../components/ui";
import { useI18n } from "../i18n";
import type { StickerSetRow } from "../types";

// Cells per modal page. Each page fully replaces the previous one (rather
// than appending to a growing "load more" list) — accumulating batches made
// the Lottie canvases overlap visually, and a fresh page also means each
// batch's animations are properly unmounted before the next one mounts.
const PAGE_SIZE = 24;

export function StickerSetPreviewModal({ set, onClose }: { set: StickerSetRow; onClose: () => void }) {
  const { t } = useI18n();
  const noun = set.Kind === "emoji" ? "emoji" : "sticker";
  const [documentIDs, setDocumentIDs] = useState<string[] | null>(null);
  const [error, setError] = useState("");
  const [page, setPage] = useState(1);

  const load = useCallback(() => {
    let cancelled = false;
    setError("");
    api.stickerSetDocuments(set.ID).then((result) => {
      if (cancelled) return;
      setDocumentIDs(result.document_ids ?? []);
    }).catch((err) => {
      if (!cancelled) setError(errorMessage(err));
    });
    return () => { cancelled = true; };
  }, [set.ID]);

  useEffect(() => {
    setDocumentIDs(null);
    setPage(1);
    return load();
  }, [load]);

  const total = documentIDs?.length ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const currentPage = Math.min(page, totalPages);
  const pageStart = (currentPage - 1) * PAGE_SIZE;
  const pageItems = documentIDs?.slice(pageStart, pageStart + PAGE_SIZE) ?? [];
  const rangeStart = pageItems.length === 0 ? 0 : pageStart + 1;
  const rangeEnd = rangeStart === 0 ? 0 : rangeStart + pageItems.length - 1;

  return createPortal(
    <div className="modal-backdrop" role="presentation">
      <section className="modal command-modal sticker-preview-modal" role="dialog" aria-modal="true" aria-label={set.Title || `#${set.ID}`}>
        <div className="modal-head">
          <div>
            <div className="eyebrow">{t("stickers.previewEyebrow")}</div>
            <h2>{set.Title || `#${set.ID}`}</h2>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} aria-label={t("action.close")}><X size={15} /></button>
        </div>
        <div className="command-body">
          <AddStickerForm setID={set.ID} noun={noun} onAdded={load} />
          {error && <Alert>{error}</Alert>}
          {!error && documentIDs === null && (
            <div className="loading-line"><Loader2 className="spin" size={18} /> {t("common.loading")}</div>
          )}
          {documentIDs !== null && total === 0 && !error && (
            <div className="empty-panel">{t("stickers.previewEmpty")}</div>
          )}
          {pageItems.length > 0 && (
            <div className="sticker-doc-grid" key={currentPage}>
              {pageItems.map((documentID) => (
                <div className="sticker-doc-grid-cell" key={documentID}>
                  <StickerDocumentPreview documentID={documentID} />
                  <ActionButton
                    compact
                    tone="danger"
                    label={t("stickers.removeSticker", { noun })}
                    icon={<Trash2 size={12} />}
                    path="/api/actions/remove-sticker-from-set"
                    payload={() => ({ set_id: set.ID, document_id: documentID })}
                    onDone={load}
                  />
                </div>
              ))}
            </div>
          )}
          {total > PAGE_SIZE && (
            <div className="gift-pager">
              <span className="gift-pager-range">{t("gifts.pageRange", { start: rangeStart, end: rangeEnd, total })}</span>
              <div className="gift-pager-controls">
                <button className="btn compact-btn" type="button" onClick={() => setPage((p) => Math.max(1, p - 1))} disabled={currentPage <= 1}>
                  <ChevronLeft size={14} /> {t("gifts.pagePrev")}
                </button>
                <span className="gift-pager-page">{t("gifts.pageOf", { page: currentPage, total: totalPages })}</span>
                <button className="btn compact-btn" type="button" onClick={() => setPage((p) => Math.min(totalPages, p + 1))} disabled={currentPage >= totalPages}>
                  {t("gifts.pageNext")} <ChevronRight size={14} />
                </button>
              </div>
            </div>
          )}
        </div>
      </section>
    </div>,
    document.body
  );
}

// Inline upload form embedded in the preview modal — file + emoji + a
// mandatory audit reason, single confirmed call (no separate dry-run preview
// step): unlike destructive actions, materializing one sticker document is
// low-risk and reversible via the per-cell Remove button.
function AddStickerForm({ setID, noun, onAdded }: { setID: string; noun: string; onAdded: () => void }) {
  const { t } = useI18n();
  const [file, setFile] = useState<File | null>(null);
  const [emoji, setEmoji] = useState("");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit() {
    if (!file) {
      setError(t("stickers.fileRequired", { noun }));
      return;
    }
    if (!emoji.trim()) {
      setError(t("stickers.emojiRequired"));
      return;
    }
    if (!reason.trim()) {
      setError(t("action.reasonRequired"));
      return;
    }
    setBusy(true);
    setError("");
    try {
      const form = new FormData();
      form.set("metadata", JSON.stringify({ command_id: "", reason: reason.trim(), confirm: true, set_id: setID, emoji: emoji.trim() }));
      form.set("file", file, file.name);
      await api.addStickerToSet(form);
      setFile(null);
      setEmoji("");
      setReason("");
      onAdded();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="sticker-add-form">
      <label className={`gift-file-picker compact ${file ? "has-file" : ""}`}>
        <input type="file" accept=".tgs,.json,.webp,application/json,application/x-tgsticker,image/webp" onChange={(event) => setFile(event.target.files?.[0] ?? null)} />
        <span className="gift-file-copy"><strong>{file ? file.name : t("stickers.filePrompt")}</strong></span>
      </label>
      <input className="small-input" value={emoji} onChange={(event) => setEmoji(event.target.value)} placeholder={t("stickers.emojiPlaceholder")} />
      <input className="small-input" value={reason} onChange={(event) => setReason(event.target.value)} placeholder={t("action.reasonPlaceholder")} />
      <button className="btn primary compact-btn" type="button" onClick={submit} disabled={busy}>
        {busy ? <Loader2 className="spin" size={14} /> : <Plus size={14} />} {t("stickers.addSticker", { noun })}
      </button>
      {error && <span className="sticker-add-form-error">{error}</span>}
    </div>
  );
}
