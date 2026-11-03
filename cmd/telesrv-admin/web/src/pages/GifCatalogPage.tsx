import { ChevronLeft, ChevronRight, ImageOff, Loader2, Plus, RefreshCw, Search, Upload, X } from "../components/icons";
import { useEffect, useMemo, useState } from "react";
import { createPortal } from "react-dom";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { Alert, Badge, EmptyRow, Metric, PageFrame, QueryPanel } from "../components/ui";
import { GIF_CATALOG_CATEGORIES, type GifCatalogRow } from "../types";

type GifPageSize = 10 | 20 | 50 | 100 | "all";

// One list-row preview cell. The backend always stores a plain H.264 MP4 (see
// files.Service.AdminUploadGifMaterial), so this is just a small looping
// <video> -- no Lottie/canvas work needed the way StickerDocumentPreview
// needs for TGS. onError falls back to a placeholder rather than a broken
// player if the document is somehow missing.
function GifPreviewThumb({ documentID }: { documentID: string }) {
  const [broken, setBroken] = useState(false);
  if (broken) {
    return (
      <div className="sticker-list-thumb-empty">
        <ImageOff size={14} />
      </div>
    );
  }
  return (
    <video
      className="gif-catalog-thumb"
      src={api.gifCatalogDocumentPreviewURL(documentID)}
      muted
      loop
      autoPlay
      playsInline
      onError={() => setBroken(true)}
    />
  );
}

// Manage view for the admin-curated GIF catalog the built-in @gif inline bot
// serves for the client's GIF picker trending/search panel.
export function GifCatalogPage() {
  const [rows, setRows] = useState<GifCatalogRow[]>([]);
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [pageSize, setPageSize] = useState<GifPageSize>(10);
  const [page, setPage] = useState(1);
  const [orderDrafts, setOrderDrafts] = useState<Record<string, string>>({});
  const [categoryDrafts, setCategoryDrafts] = useState<Record<string, string>>({});
  const [createOpen, setCreateOpen] = useState(false);

  async function load() {
    setBusy(true);
    setError("");
    try {
      setRows((await api.gifCatalog()).rows ?? []);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => { void load(); }, []);

  const visible = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    if (!normalized) return rows;
    return rows.filter((row) =>
      row.ID.includes(normalized) || row.Title.toLowerCase().includes(normalized)
    );
  }, [rows, query]);

  useEffect(() => { setPage(1); }, [query, pageSize]);

  const totalPages = pageSize === "all" ? 1 : Math.max(1, Math.ceil(visible.length / pageSize));
  const currentPage = Math.min(page, totalPages);
  const paged = useMemo(() => {
    if (pageSize === "all") return visible;
    const start = (currentPage - 1) * pageSize;
    return visible.slice(start, start + pageSize);
  }, [visible, currentPage, pageSize]);
  const rangeStart = paged.length === 0 ? 0 : pageSize === "all" ? 1 : (currentPage - 1) * pageSize + 1;
  const rangeEnd = rangeStart === 0 ? 0 : rangeStart + paged.length - 1;

  const counts = useMemo(() => ({
    total: rows.length,
    enabled: rows.filter((row) => row.Enabled).length,
    uncategorized: rows.filter((row) => !row.Category).length
  }), [rows]);

  return (
    <PageFrame
      title={"GIFs"}
      eyebrow={"Curated GIFs served by @gif in the client's GIF picker (trending + search)"}
      actions={
        <>
          <button className="btn" type="button" onClick={() => load()} disabled={busy}>
            <RefreshCw size={15} /> {"Refresh"}
          </button>
          <ActionButton
            tone="neutral"
            label={"Auto-categorize"}
            path="/api/actions/auto-categorize-gif-catalog"
            payload={() => ({})}
            onDone={() => void load()}
          />
          <ActionButton
            tone="danger"
            label={"Delete uncategorized"}
            path="/api/actions/delete-uncategorized-gifs"
            payload={() => ({})}
            onDone={() => void load()}
          />
          <button className="btn primary" type="button" onClick={() => setCreateOpen(true)}>
            <Plus size={15} /> {"Add GIF"}
          </button>
        </>
      }
    >
      {error && <Alert>{error}</Alert>}
      <div className="metric-row">
        <Metric label={"Total GIFs"} value={String(counts.total)} />
        <Metric label={"Enabled"} value={String(counts.enabled)} tone="good" />
        <Metric label={"Uncategorized"} value={String(counts.uncategorized)} tone={counts.uncategorized > 0 ? "warn" : undefined} />
      </div>
      <QueryPanel>
        <div className="toolbar">
          <label className="searchbox">
            <Search size={15} />
            <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={"Search ID or title"} />
          </label>
          <label className="gift-page-size">
            <span>{"Per page"}</span>
            <select
              value={String(pageSize)}
              onChange={(event) => setPageSize(event.target.value === "all" ? "all" : (Number(event.target.value) as GifPageSize))}
            >
              <option value="10">10</option>
              <option value="20">20</option>
              <option value="50">50</option>
              <option value="100">100</option>
              <option value="all">{"All"}</option>
            </select>
          </label>
          <span className="gift-list-summary">{`Showing ${visible.length} of ${rows.length}`}</span>
        </div>
      </QueryPanel>
      <div className="table-wrap gift-table-wrap">
        <table className="data-table">
          <thead>
            <tr>
              <th>{"Preview"}</th>
              <th>{"ID"}</th>
              <th>{"Title"}</th>
              <th>{"Document ID"}</th>
              <th>{"Added by"}</th>
              <th>{"Status"}</th>
              <th>{"Category"}</th>
              <th>{"Sort order"}</th>
              <th>{"Actions"}</th>
            </tr>
          </thead>
          <tbody>
            {paged.map((row) => (
              <tr className={row.Enabled ? "" : "gift-row-disabled"} key={row.ID}>
                <td><GifPreviewThumb documentID={row.DocumentID} /></td>
                <td className="mono">{row.ID}</td>
                <td>{row.Title || <span className="muted-cell">{"Untitled"}</span>}</td>
                <td className="mono">{row.DocumentID}</td>
                <td>{row.CreatedBy || <span className="muted-cell">{"—"}</span>}</td>
                <td>{!row.Enabled && <Badge tone="danger">{"Disabled"}</Badge>}</td>
                <td>
                  <div className="sort-order-editor">
                    <select
                      className="small-input"
                      value={categoryDrafts[row.ID] ?? row.Category}
                      onChange={(event) => setCategoryDrafts((prev) => ({ ...prev, [row.ID]: event.target.value }))}
                    >
                      <option value="">{"Uncategorized"}</option>
                      {GIF_CATALOG_CATEGORIES.map((category) => (
                        <option key={category} value={category}>{category}</option>
                      ))}
                    </select>
                    <ActionButton
                      compact
                      tone="neutral"
                      label={"Save"}
                      path="/api/actions/set-gif-catalog-category"
                      payload={() => ({ id: row.ID, category: categoryDrafts[row.ID] ?? row.Category })}
                      onDone={() => void load()}
                    />
                  </div>
                </td>
                <td>
                  <div className="sort-order-editor">
                    <input
                      type="number"
                      className="small-input"
                      value={orderDrafts[row.ID] ?? String(row.SortOrder)}
                      onChange={(event) => setOrderDrafts((prev) => ({ ...prev, [row.ID]: event.target.value }))}
                    />
                    <ActionButton
                      compact
                      tone="neutral"
                      label={"Save"}
                      path="/api/actions/set-gif-catalog-sort-order"
                      payload={() => ({ id: row.ID, sort_order: Number(orderDrafts[row.ID] ?? row.SortOrder) })}
                      onDone={() => void load()}
                    />
                  </div>
                </td>
                <td>
                  <div className="gift-table-actions">
                    <ActionButton
                      compact
                      tone="neutral"
                      label={row.Enabled ? "Disable" : "Enable"}
                      path="/api/actions/set-gif-catalog-enabled"
                      payload={() => ({ id: row.ID, enabled: !row.Enabled })}
                      onDone={() => void load()}
                    />
                    <ActionButton
                      compact
                      tone="danger"
                      label={"Delete"}
                      path="/api/actions/delete-gif-catalog-entry"
                      payload={() => ({ id: row.ID })}
                      onDone={() => void load()}
                    />
                  </div>
                </td>
              </tr>
            ))}
            {paged.length === 0 && <EmptyRow colSpan={9} />}
          </tbody>
        </table>
      </div>
      {pageSize !== "all" && visible.length > 0 && (
        <div className="gift-pager">
          <span className="gift-pager-range">{`Showing ${rangeStart}-${rangeEnd} of ${visible.length}`}</span>
          <div className="gift-pager-controls">
            <button className="btn compact-btn" type="button" onClick={() => setPage((p) => Math.max(1, p - 1))} disabled={currentPage <= 1}>
              <ChevronLeft size={14} /> {"Previous"}
            </button>
            <span className="gift-pager-page">{`Page ${currentPage} of ${totalPages}`}</span>
            <button className="btn compact-btn" type="button" onClick={() => setPage((p) => Math.min(totalPages, p + 1))} disabled={currentPage >= totalPages}>
              {"Next"} <ChevronRight size={14} />
            </button>
          </div>
        </div>
      )}
      {createOpen && <AddGifModal onClose={() => setCreateOpen(false)} onCreated={() => void load()} />}
    </PageFrame>
  );
}

function AddGifModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [title, setTitle] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [previewURL, setPreviewURL] = useState<string | null>(null);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  function pickFile(picked: File | null) {
    setFile(picked);
    setPreviewURL((prev) => {
      if (prev) URL.revokeObjectURL(prev);
      return picked ? URL.createObjectURL(picked) : null;
    });
  }

  async function submit() {
    if (!title.trim() || !file) {
      setError("Title and a GIF/MP4 file are required.");
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
      form.set("metadata", JSON.stringify({ command_id: "", reason: reason.trim(), confirm: true, title: title.trim() }));
      form.set("file", file, file.name);
      await api.createGifCatalogEntry(form);
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
      <section className="modal command-modal" role="dialog" aria-modal="true" aria-label={"Add a GIF"}>
        <div className="modal-head">
          <div>
            <div className="eyebrow">{"New catalog entry"}</div>
            <h2>{"Add a GIF"}</h2>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} disabled={busy} aria-label={"Close"}><X size={15} /></button>
        </div>
        <div className="command-body">
          <div className="gift-fields-grid">
            <label><span>{"Title"}</span><input value={title} maxLength={128} onChange={(event) => setTitle(event.target.value)} /></label>
          </div>
          <label className={`gift-file-picker ${file ? "has-file" : ""}`}>
            <input type="file" accept=".gif,.mp4,image/gif,video/mp4" onChange={(event) => pickFile(event.target.files?.[0] ?? null)} />
            <span className="gift-file-copy"><span className="gift-field-label">{"File"}</span><strong>{file ? file.name : "Choose a GIF or MP4 file"}</strong></span>
            <span className="gift-file-action">{file ? "Change file" : "Choose file"}</span>
          </label>
          {previewURL && (
            <div className="gif-catalog-preview">
              {file?.type === "video/mp4" ? (
                <video src={previewURL} autoPlay loop muted playsInline />
              ) : (
                <img src={previewURL} alt="" />
              )}
            </div>
          )}
          <label className="gift-reason-field"><span>{"Audit reason"}</span><input value={reason} placeholder={"Briefly describe why this GIF is being added"} onChange={(event) => setReason(event.target.value)} /></label>
          {error && <Alert>{error}</Alert>}
        </div>
        <div className="modal-actions">
          <button className="btn" type="button" onClick={onClose} disabled={busy}>{"Close"}</button>
          <button className="btn primary" type="button" onClick={submit} disabled={busy}>
            {busy ? <Loader2 className="spin" size={15} /> : <Upload size={15} />}
            {"Add GIF"}
          </button>
        </div>
      </section>
    </div>,
    document.body
  );
}
