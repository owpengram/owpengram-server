import { ChevronLeft, ChevronRight, Loader2, X } from "lucide-react";
import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { api, errorMessage } from "../api";
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
  const [documentIDs, setDocumentIDs] = useState<string[] | null>(null);
  const [error, setError] = useState("");
  const [page, setPage] = useState(1);

  useEffect(() => {
    let cancelled = false;
    setDocumentIDs(null);
    setError("");
    setPage(1);
    api.stickerSetDocuments(set.ID).then((result) => {
      if (cancelled) return;
      setDocumentIDs(result.document_ids ?? []);
    }).catch((err) => {
      if (!cancelled) setError(errorMessage(err));
    });
    return () => { cancelled = true; };
  }, [set.ID]);

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
          {error && <Alert>{error}</Alert>}
          {!error && documentIDs === null && (
            <div className="loading-line"><Loader2 className="spin" size={18} /> {t("common.loading")}</div>
          )}
          {documentIDs !== null && total === 0 && !error && (
            <div className="empty-panel">{t("stickers.previewEmpty")}</div>
          )}
          {pageItems.length > 0 && (
            <div className="sticker-doc-grid" key={currentPage}>
              {pageItems.map((documentID) => <StickerDocumentPreview key={documentID} documentID={documentID} />)}
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
