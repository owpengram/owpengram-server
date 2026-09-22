import { Boxes, CheckCircle2, ChevronLeft, ChevronRight, FileJson2, FileArchive, Gem, Loader2, PackagePlus, Pause, Play, Plus, RefreshCw, Search, ShieldCheck, Upload, X } from "lucide-react";
import lottie from "lottie-web/build/player/lottie_light_canvas";
import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { Alert, Badge, EmptyRow, Metric, PageFrame, QueryPanel } from "../components/ui";
import { formatDate } from "../lib/format";
import type { CommandResult, GiftPackSummary, StarGiftCatalogRow } from "../types";
import { GiftCollectiblesModal } from "./GiftCollectiblesModal";

type GiftPageSize = 10 | 20 | 50 | 100 | "all";

function formatBytes(value: number | string) {
  const bytes = Number(value);
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

// Looping animated preview with a play/pause toggle -- used both in the
// catalog table (compact) and standalone. Renders whatever
// api.giftAnimation resolves to, straight from the active revision.
export function LottiePreview({ giftID, revision, compact = false }: { giftID: string; revision: number; compact?: boolean }) {
  const host = useRef<HTMLDivElement>(null);
  const animation = useRef<ReturnType<typeof lottie.loadAnimation> | null>(null);
  const [playing, setPlaying] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    api.giftAnimation(giftID).then((data) => {
      if (cancelled || !host.current) return;
      animation.current?.destroy();
      animation.current = lottie.loadAnimation({
        container: host.current,
        renderer: "canvas",
        loop: true,
        autoplay: true,
        animationData: structuredClone(data)
      });
    }).catch((err) => setError(errorMessage(err)));
    return () => {
      cancelled = true;
      animation.current?.destroy();
      animation.current = null;
    };
  }, [giftID, revision]);

  function toggle() {
    if (!animation.current) return;
    if (playing) animation.current.pause();
    else animation.current.play();
    setPlaying(!playing);
  }

  return (
    <div className={`gift-animation-shell ${compact ? "compact" : ""}`}>
      <div className="gift-animation" ref={host}>{error && <span>{error}</span>}</div>
      <button className="gift-play" type="button" onClick={toggle} aria-label={playing ? "Pause" : "Play"}>
        {playing ? <Pause size={14} /> : <Play size={14} />}
      </button>
    </div>
  );
}

// Manage view for the StarGift storefront's catalog -- authoring a plain gift
// from an uploaded animation, publishing its collectible (unique-upgrade)
// pool, and its storefront visibility/order. Auction and craft authoring are
// a separate, much larger use case this deliberately does not cover -- see
// internal/admin.StarGiftCatalogService's doc comment.
type CatalogTab = "catalog" | "import";

export function StarGiftCatalogPage() {
  const [tab, setTab] = useState<CatalogTab>("catalog");
  const [gifts, setGifts] = useState<StarGiftCatalogRow[]>([]);
  const [query, setQuery] = useState("");
  const [importOpen, setImportOpen] = useState(false);
  const [collectibleGift, setCollectibleGift] = useState<StarGiftCatalogRow | null>(null);
  const [file, setFile] = useState<File | null>(null);
  const [giftID, setGiftID] = useState("0");
  const [title, setTitle] = useState("");
  const [stars, setStars] = useState("50");
  const [convertStars, setConvertStars] = useState("50");
  const [sortOrder, setSortOrder] = useState("0");
  const [enabled, setEnabled] = useState(true);
  const [reason, setReason] = useState("");
  const [preview, setPreview] = useState<CommandResult | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [importError, setImportError] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [bulkReason, setBulkReason] = useState("");
  const [bulkBusy, setBulkBusy] = useState(false);
  const [bulkError, setBulkError] = useState("");
  const [pageSize, setPageSize] = useState<GiftPageSize>(10);
  const [page, setPage] = useState(1);

  const [defaultPack, setDefaultPack] = useState<GiftPackSummary[]>([]);
  const [defaultPackError, setDefaultPackError] = useState("");
  const [packFile, setPackFile] = useState<File | null>(null);
  const [packReason, setPackReason] = useState("");
  const [packPreview, setPackPreview] = useState<CommandResult | null>(null);
  const [packBusy, setPackBusy] = useState(false);
  const [packError, setPackError] = useState("");

  useEffect(() => {
    if (tab !== "import") return;
    api.defaultGiftPack().then((res) => setDefaultPack(res.gifts ?? [])).catch((err) => setDefaultPackError(errorMessage(err)));
  }, [tab]);

  function packUploadForm(confirm: boolean, commandID = "") {
    if (!packFile) throw new Error("Choose a pack .zip file first");
    if (!packReason.trim()) throw new Error("Please enter an operation reason");
    const form = new FormData();
    form.set("metadata", JSON.stringify({ command_id: commandID, reason: packReason.trim(), confirm }));
    form.set("file", packFile, packFile.name);
    return form;
  }

  async function validatePackImport() {
    setPackBusy(true); setPackError(""); setPackPreview(null);
    try {
      setPackPreview(await api.importGiftPack(packUploadForm(false)));
    } catch (err) {
      setPackError(errorMessage(err));
    } finally { setPackBusy(false); }
  }

  async function confirmPackImport() {
    if (!packPreview) return;
    setPackBusy(true); setPackError("");
    try {
      await api.importGiftPack(packUploadForm(true, packPreview.command_id));
      setPackPreview(null); setPackFile(null); setPackReason("");
      await load();
    } catch (err) {
      setPackError(errorMessage(err));
    } finally { setPackBusy(false); }
  }

  async function load() {
    setError("");
    try {
      setGifts((await api.starGiftCatalog()).rows ?? []);
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  useEffect(() => { void load(); }, []);

  const visibleGifts = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    if (!normalized) return gifts;
    return gifts.filter((gift) =>
      String(gift.GiftID).includes(normalized) ||
      gift.Title.toLowerCase().includes(normalized) ||
      gift.SourceFormat.toLowerCase().includes(normalized)
    );
  }, [gifts, query]);

  useEffect(() => { setPage(1); }, [query, pageSize]);

  const totalPages = pageSize === "all" ? 1 : Math.max(1, Math.ceil(visibleGifts.length / pageSize));
  const currentPage = Math.min(page, totalPages);
  const pagedGifts = useMemo(() => {
    if (pageSize === "all") return visibleGifts;
    const start = (currentPage - 1) * pageSize;
    return visibleGifts.slice(start, start + pageSize);
  }, [visibleGifts, currentPage, pageSize]);
  const pageRangeStart = pagedGifts.length === 0 ? 0 : pageSize === "all" ? 1 : (currentPage - 1) * pageSize + 1;
  const pageRangeEnd = pageRangeStart === 0 ? 0 : pageRangeStart + pagedGifts.length - 1;

  const allVisibleSelected = pagedGifts.length > 0 && pagedGifts.every((gift) => selected.has(gift.GiftID));

  function toggleSelected(id: string) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  function toggleSelectAllVisible() {
    setSelected((prev) => {
      if (allVisibleSelected) {
        const next = new Set(prev);
        for (const gift of pagedGifts) next.delete(gift.GiftID);
        return next;
      }
      const next = new Set(prev);
      for (const gift of pagedGifts) next.add(gift.GiftID);
      return next;
    });
  }

  async function bulkSetEnabled(nextEnabled: boolean) {
    if (!bulkReason.trim()) {
      setBulkError("Please enter an operation reason");
      return;
    }
    setBulkBusy(true);
    setBulkError("");
    const ids = Array.from(selected);
    let failed = 0;
    for (const id of ids) {
      try {
        await api.action("/api/actions/set-star-gift-catalog-enabled", {
          gift_id: id,
          enabled: nextEnabled,
          reason: bulkReason.trim(),
          confirm: true
        });
      } catch {
        failed++;
      }
    }
    setBulkBusy(false);
    if (failed > 0) {
      setBulkError(`${failed} of ${ids.length} failed`);
    } else {
      setSelected(new Set());
      setBulkReason("");
    }
    await load();
  }

  function uploadForm(confirm: boolean, commandID = "") {
    if (!file) throw new Error("Choose a TGS or Lottie file first");
    if (!reason.trim()) throw new Error("Please enter an operation reason");
    const form = new FormData();
    form.set("metadata", JSON.stringify({
      command_id: commandID,
      reason: reason.trim(),
      confirm,
      gift_id: giftID,
      title: title.trim(),
      stars,
      convert_stars: convertStars,
      enabled,
      sort_order: Number(sortOrder)
    }));
    form.set("file", file, file.name);
    return form;
  }

  async function validateImport() {
    setBusy(true); setImportError(""); setPreview(null);
    try {
      setPreview(await api.createStarGiftCatalogEntry(uploadForm(false)));
    } catch (err) {
      setImportError(errorMessage(err));
    } finally { setBusy(false); }
  }

  async function confirmImport() {
    if (!preview) return;
    setBusy(true); setImportError("");
    try {
      await api.createStarGiftCatalogEntry(uploadForm(true, preview.command_id));
      setPreview(null); setFile(null); setGiftID("0"); setTitle("");
      await load();
      setImportOpen(false);
    } catch (err) {
      setImportError(errorMessage(err));
    } finally { setBusy(false); }
  }

  function startImport() {
    setGiftID("0"); setTitle(""); setStars("50"); setConvertStars("50"); setSortOrder("0");
    setEnabled(true); setReason(""); setFile(null); setPreview(null); setImportError("");
    setImportOpen(true);
  }

  function startRevision(gift: StarGiftCatalogRow) {
    setGiftID(gift.GiftID); setTitle(gift.Title); setStars(String(gift.Stars));
    setConvertStars(String(gift.ConvertStars)); setSortOrder(String(gift.SortOrder)); setEnabled(gift.Enabled);
    setReason(""); setFile(null); setPreview(null); setImportError("");
    setImportOpen(true);
  }

  return (
    <PageFrame title={"Star Gift Catalog"} eyebrow={"Catalog, immutable revisions and animation assets"} actions={<>
      <button className="btn" type="button" onClick={() => load()} disabled={busy}><RefreshCw size={15} /> {"Refresh"}</button>
      <button className="btn primary" type="button" onClick={startImport}><Plus size={15} /> {"Add gift"}</button>
    </>}>
      {error && <Alert>{error}</Alert>}
      <div className="toolbar" role="group" aria-label={"Star gift catalog sections"}>
        <button className={`btn icon-text ${tab === "catalog" ? "primary" : ""}`} type="button" aria-pressed={tab === "catalog"} onClick={() => setTab("catalog")}>
          <Gem size={15} /> {"Catalog"}
        </button>
        <button className={`btn icon-text ${tab === "import" ? "primary" : ""}`} type="button" aria-pressed={tab === "import"} onClick={() => setTab("import")}>
          <PackagePlus size={15} /> {"Import Pack"}
        </button>
      </div>
      {tab === "catalog" && <>
      <div className="metric-row gift-metrics">
        <Metric label={"Catalog entries"} value={String(gifts.length)} />
        <Metric label={"Enabled"} value={String(gifts.filter((gift) => gift.Enabled).length)} tone="good" />
        <Metric label={"Received gifts"} value={String(gifts.reduce((sum, gift) => sum + Number(gift.ReceivedCount), 0))} />
        <Metric label={"Accepted formats"} value="TGS / Lottie" />
      </div>
      <QueryPanel>
        <div className="toolbar">
          <label className="searchbox"><Search size={15} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={"Search gift ID, title or format"} /></label>
          <label className="gift-page-size"><span>{"Per page"}</span>
            <select value={String(pageSize)} onChange={(event) => setPageSize(event.target.value === "all" ? "all" : (Number(event.target.value) as GiftPageSize))}>
              <option value="10">10</option>
              <option value="20">20</option>
              <option value="50">50</option>
              <option value="100">100</option>
              <option value="all">{"All"}</option>
            </select>
          </label>
          <span className="gift-list-summary">{`Showing ${visibleGifts.length} of ${gifts.length}`}</span>
        </div>
      </QueryPanel>
      {selected.size > 0 && <div className="gift-bulk-toolbar">
        <span className="gift-bulk-count">{`${selected.size} selected`}</span>
        <label className="gift-reason-field gift-bulk-reason"><span>{"Audit reason"}</span><input value={bulkReason} placeholder={"Briefly describe why this gift is being changed"} onChange={(e) => setBulkReason(e.target.value)} /></label>
        <button className="btn" type="button" onClick={() => bulkSetEnabled(true)} disabled={bulkBusy}>
          {bulkBusy ? <Loader2 className="spin" size={14} /> : <CheckCircle2 size={14} />} {"Enable selected"}
        </button>
        <button className="btn" type="button" onClick={() => bulkSetEnabled(false)} disabled={bulkBusy}>
          {bulkBusy ? <Loader2 className="spin" size={14} /> : <Pause size={14} />} {"Disable selected"}
        </button>
        <button className="btn" type="button" onClick={() => { setSelected(new Set()); setBulkError(""); }} disabled={bulkBusy}>{"Close"}</button>
        {bulkError && <span className="gift-bulk-error">{bulkError}</span>}
      </div>}
      <div className="table-wrap gift-table-wrap">
        <table className="data-table gift-table">
          <thead><tr><th className="gift-select-col"><input type="checkbox" checked={allVisibleSelected} onChange={toggleSelectAllVisible} aria-label={"Select all visible gifts"} /></th><th>{"Animation file"}</th><th>{"ID / Revision"}</th><th>{"Display title"}</th><th>{"Price / Conversion"}</th><th>{"Source"}</th><th>{"Received gifts"}</th><th>{"Status"}</th><th>{"Updated"}</th><th>{"Actions"}</th></tr></thead>
          <tbody>
            {pagedGifts.map((gift) => (
              <tr className={gift.Enabled ? "" : "gift-row-disabled"} key={gift.GiftID}>
                <td className="gift-select-col"><input type="checkbox" checked={selected.has(gift.GiftID)} onChange={() => toggleSelected(gift.GiftID)} aria-label={`Select gift ${gift.GiftID}`} /></td>
                <td><LottiePreview giftID={gift.GiftID} revision={gift.Revision} compact /></td>
                <td className="mono">{gift.GiftID} / {gift.Revision}</td>
                <td><strong className="gift-table-title">{gift.Title || `Gift #${gift.GiftID}`}</strong><span className="gift-sort-order">{"Sort order"}: {gift.SortOrder}</span></td>
                <td><strong className="gift-table-price">⭐ {gift.Stars}</strong><span className="gift-convert-price">→ {gift.ConvertStars}</span></td>
                <td><Badge>{gift.SourceFormat}</Badge><span className="gift-source-size">{gift.Width}×{gift.Height}</span></td>
                <td>{gift.ReceivedCount}</td>
                <td><Badge tone={gift.Enabled ? "good" : "neutral"}>{gift.Enabled ? "Enabled" : "Disabled"}</Badge></td>
                <td>{formatDate(gift.UpdatedAt)}</td>
                <td><div className="gift-table-actions"><button className="btn compact-btn collectible-button" type="button" onClick={() => setCollectibleGift(gift)}><Gem size={13} />{"Attribute pool"}</button><button className="btn compact-btn" type="button" onClick={() => startRevision(gift)}>{"New revision"}</button><ActionButton compact tone="neutral" label={gift.Enabled ? "Disable" : "Enable"} path="/api/actions/set-star-gift-catalog-enabled" payload={() => ({ gift_id: gift.GiftID, enabled: !gift.Enabled })} onDone={() => void load()} /></div></td>
              </tr>
            ))}
            {pagedGifts.length === 0 && <EmptyRow colSpan={10} />}
          </tbody>
        </table>
      </div>
      {pageSize !== "all" && visibleGifts.length > 0 && <div className="gift-pager">
        <span className="gift-pager-range">{`Showing ${pageRangeStart}-${pageRangeEnd} of ${visibleGifts.length}`}</span>
        <div className="gift-pager-controls">
          <button className="btn compact-btn" type="button" onClick={() => setPage((p) => Math.max(1, p - 1))} disabled={currentPage <= 1}>
            <ChevronLeft size={14} /> {"Previous"}
          </button>
          <span className="gift-pager-page">{`Page ${currentPage} of ${totalPages}`}</span>
          <button className="btn compact-btn" type="button" onClick={() => setPage((p) => Math.min(totalPages, p + 1))} disabled={currentPage >= totalPages}>
            {"Next"} <ChevronRight size={14} />
          </button>
        </div>
      </div>}
      </>}

      {tab === "import" && <>
      <section className="section-block">
        <h2>{"Default pack"}</h2>
        <div className="card-body">
          <p className="gift-import-note"><span>{"OwpenGram's own built-in gift pack -- 7 original gifts covering every mechanic (plain purchase, standard upgrade, limited supply, craft, resale floor, birthday, premium-required, support-only, auction), safe to import on any deployment."}</span></p>
          {defaultPackError && <Alert>{defaultPackError}</Alert>}
          <div className="table-wrap gift-table-wrap">
            <table className="data-table gift-table">
              <thead><tr><th>{"Title"}</th><th>{"Flags"}</th></tr></thead>
              <tbody>
                {defaultPack.map((gift) => (
                  <tr key={gift.theme}>
                    <td><strong className="gift-table-title">{gift.title}</strong></td>
                    <td>{(gift.flags ?? []).map((flag) => <Badge key={flag}>{flag}</Badge>)}</td>
                  </tr>
                ))}
                {defaultPack.length === 0 && <EmptyRow colSpan={2} />}
              </tbody>
            </table>
          </div>
          <ActionButton
            tone="primary"
            label={"Import default pack"}
            icon={<Boxes size={15} />}
            path="/api/actions/import-default-gift-pack"
            payload={() => ({})}
            onDone={() => void load()}
          />
        </div>
      </section>

      <section className="section-block">
        <h2>{"Upload a pack"}</h2>
        <div className="card-body">
          <p className="gift-import-note"><span>{"A community-authored pack: a .zip with pack.json at the root plus the .tgs/Lottie assets it references. See "}<code>{"docs/gift-packs.md"}</code>{" for the format and its rlottie caveats."}</span></p>
          <label className={`gift-file-picker ${packFile ? "has-file" : ""}`}>
            <input type="file" accept=".zip,application/zip" onChange={(e) => { setPackFile(e.target.files?.[0] ?? null); setPackPreview(null); }} />
            <span className="gift-file-icon"><FileArchive size={22} /></span>
            <span className="gift-file-copy"><span className="gift-field-label">{"Pack archive"}</span><strong>{packFile ? packFile.name : "Drop or choose a pack .zip"}</strong><small>{packFile ? formatBytes(packFile.size) : "pack.json + assets, validated before import"}</small></span>
            <span className="gift-file-action">{packFile ? "Change file" : "Choose file"}</span>
          </label>
          <label className="gift-reason-field"><span>{"Audit reason"}</span><input value={packReason} placeholder={"Briefly describe why this pack is being imported"} onChange={(e) => setPackReason(e.target.value)} /></label>
          {packError && <Alert>{packError}</Alert>}
          {packPreview && <div className="gift-validation">
            <div className="gift-validation-head"><CheckCircle2 size={17} /><div><strong>{"Validation passed"}</strong><span>{"Review what would be imported, then confirm."}</span></div></div>
            <pre>{JSON.stringify(packPreview.details, null, 2)}</pre>
          </div>}
          <div className="action-stack">
            <button className="btn" type="button" onClick={validatePackImport} disabled={packBusy}>
              {packBusy ? <Loader2 className="spin" size={15} /> : <ShieldCheck size={15} />} {"Dry-run validation"}
            </button>
            <button className="btn primary" type="button" onClick={confirmPackImport} disabled={packBusy || !packPreview}>
              <Upload size={15} /> {"Confirm import"}
            </button>
          </div>
        </div>
      </section>
      </>}

      {importOpen && createPortal(
        <div className="modal-backdrop" role="presentation">
          <section className="modal command-modal gift-import-modal" role="dialog" aria-modal="true" aria-label={giftID !== "0" ? `Create revision for gift #${giftID}` : "Import a Star Gift"}>
            <div className="modal-head">
              <div><div className="eyebrow">{"Gift catalog operation"}</div><h2>{giftID !== "0" ? `Create revision for gift #${giftID}` : "Import a Star Gift"}</h2></div>
              <button className="icon-btn" type="button" onClick={() => setImportOpen(false)} disabled={busy} aria-label={"Close"}><X size={15} /></button>
            </div>
            <div className="command-body gift-import-modal-body">
              <div className="command-steps">
                <div className={`command-step ${file ? "done" : "active"}`}><span>1</span><strong>{"File and details"}</strong></div>
                <div className={`command-step ${preview ? "done" : file ? "active" : ""}`}><span>2</span><strong>{"Dry-run validation"}</strong></div>
                <div className={`command-step ${preview ? "active" : ""}`}><span>3</span><strong>{"Confirm import"}</strong></div>
              </div>
              <div className="gift-import-note"><span>{"Upload TGS or plain Lottie JSON. Lottie is normalized and compressed to TGS."}</span><div className="gift-format-chips" aria-label={"Accepted formats"}><span>TGS</span><span>Lottie JSON</span></div></div>
              <label className={`gift-file-picker ${file ? "has-file" : ""}`}>
                <input type="file" accept=".tgs,.json,.lottie,application/json,application/x-tgsticker" onChange={(e) => { setFile(e.target.files?.[0] ?? null); setPreview(null); }} />
                <span className="gift-file-icon"><FileJson2 size={22} /></span>
                <span className="gift-file-copy"><span className="gift-field-label">{"Animation file"}</span><strong>{file ? file.name : "Drop or choose a TGS / Lottie file"}</strong><small>{file ? formatBytes(file.size) : "TGS, JSON or Lottie · validated before import"}</small></span>
                <span className="gift-file-action">{file ? "Change file" : "Choose file"}</span>
              </label>
              <div className="gift-fields-grid">
                <label><span>{"Display title"}</span><input value={title} maxLength={128} placeholder={"e.g. Celebration Star"} onChange={(e) => { setTitle(e.target.value); setPreview(null); }} /></label>
                <label><span>{"Price in Stars"}</span><input type="number" min="1" value={stars} onChange={(e) => { setStars(e.target.value); setPreview(null); }} /></label>
                <label><span>{"Conversion Stars"}</span><input type="number" min="0" value={convertStars} onChange={(e) => { setConvertStars(e.target.value); setPreview(null); }} /></label>
                <label><span>{"Sort order"}</span><input type="number" value={sortOrder} onChange={(e) => { setSortOrder(e.target.value); setPreview(null); }} /></label>
              </div>
              <label className="gift-switch"><input type="checkbox" checked={enabled} onChange={(e) => { setEnabled(e.target.checked); setPreview(null); }} /><span className="gift-switch-track" aria-hidden="true"><span /></span><span>{"Enable after import"}</span></label>
              <label className="gift-reason-field"><span>{"Audit reason"}</span><input value={reason} placeholder={"Briefly describe why this gift is being imported"} onChange={(e) => setReason(e.target.value)} /></label>
              {importError && <Alert>{importError}</Alert>}
              {preview && <div className="gift-validation"><div className="gift-validation-head"><CheckCircle2 size={17} /><div><strong>{"Validation passed"}</strong><span>{"Review the normalized metadata, then confirm the import."}</span></div></div><pre>{JSON.stringify(preview.details, null, 2)}</pre></div>}
            </div>
            <div className="modal-actions">
              <button className="btn" type="button" onClick={() => setImportOpen(false)} disabled={busy}>{"Close"}</button>
              <button className="btn" type="button" onClick={validateImport} disabled={busy}>{busy ? <Loader2 className="spin" size={15} /> : <ShieldCheck size={15} />}{"Dry-run validation"}</button>
              <button className="btn primary" type="button" onClick={confirmImport} disabled={busy || !preview}><Upload size={15} />{"Confirm import"}</button>
            </div>
          </section>
        </div>,
        document.body
      )}
      {collectibleGift && <GiftCollectiblesModal gift={collectibleGift} onClose={() => setCollectibleGift(null)} onPublished={() => void load()} />}
    </PageFrame>
  );
}
