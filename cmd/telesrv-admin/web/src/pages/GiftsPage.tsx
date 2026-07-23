import { CheckCircle2, ChevronLeft, ChevronRight, FileJson2, Gem, Loader2, Pause, Play, Plus, RefreshCw, Search, ShieldCheck, Upload, X } from "lucide-react";
import lottie from "lottie-web/build/player/lottie_light_canvas";
import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { api, APIError, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { Alert, Badge, EmptyRow, Metric, PageFrame, QueryPanel } from "../components/ui";
import { useI18n } from "../i18n";
import { formatDate } from "../lib/format";
import type { CommandResult, DefaultGiftRow, OfficialStarGiftRow, StarGiftRow } from "../types";
import { GiftCollectiblesModal } from "./GiftCollectiblesModal";

type OfficialGiftCategory = "all" | "upgrade" | "craft" | "basic";
type GiftPageSize = 10 | 20 | 50 | 100 | "all";

// The demo pool only has 3 placeholder gifts left after pruning to one per
// capability tier (Spark/Star/Coin); hide the tab until real custom designs
// replace them. Flip back to true to re-enable.
const SHOW_DEFAULT_GIFTS_TAB = false;

function defaultGiftAttributeCount(gift: DefaultGiftRow) {
  return gift.model_count + gift.pattern_count + gift.backdrop_count;
}

function officialGiftAttributeCount(gift: OfficialStarGiftRow) {
  return gift.model_count + gift.pattern_count + gift.backdrop_count;
}

function formatBytes(value: number | string) {
	const bytes = Number(value);
	if (bytes < 1024) return `${bytes} B`;
	if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
	return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function LottiePreview({ giftID, revision, compact = false }: { giftID: string; revision: number; compact?: boolean }) {
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

function DefaultLottiePreview({ id }: { id: number }) {
  const host = useRef<HTMLDivElement>(null);
  useEffect(() => {
    let cancelled = false;
    let player: ReturnType<typeof lottie.loadAnimation> | null = null;
    api.defaultGiftAnimation(id).then((data) => {
      if (cancelled || !host.current) return;
      player = lottie.loadAnimation({ container: host.current, renderer: "canvas", loop: true, autoplay: true, animationData: structuredClone(data) });
    }).catch(() => undefined);
    return () => { cancelled = true; player?.destroy(); };
  }, [id]);
  return <div className="gift-animation-shell"><div className="gift-animation" ref={host} /></div>;
}

function OfficialLottiePreview({ sourceGiftID }: { sourceGiftID: string }) {
  const host = useRef<HTMLDivElement>(null);
  useEffect(() => {
    let cancelled = false;
    let player: ReturnType<typeof lottie.loadAnimation> | null = null;
    api.officialGiftAnimation(sourceGiftID).then((data) => {
      if (cancelled || !host.current) return;
      player = lottie.loadAnimation({ container: host.current, renderer: "canvas", loop: true, autoplay: true, animationData: structuredClone(data) });
    }).catch(() => undefined);
    return () => { cancelled = true; player?.destroy(); };
  }, [sourceGiftID]);
  return <div className="gift-animation-shell"><div className="gift-animation" ref={host} /></div>;
}

export function GiftsPage() {
  const { t } = useI18n();
  const [gifts, setGifts] = useState<StarGiftRow[]>([]);
  const [query, setQuery] = useState("");
  const [importOpen, setImportOpen] = useState(false);
  const [collectibleGift, setCollectibleGift] = useState<StarGiftRow | null>(null);
  const [file, setFile] = useState<File | null>(null);
  const [importSource, setImportSource] = useState<"default" | "official" | "file">(SHOW_DEFAULT_GIFTS_TAB ? "default" : "official");
  const [defaultGifts, setDefaultGifts] = useState<DefaultGiftRow[]>([]);
  const [selectedDefaultID, setSelectedDefaultID] = useState(0);
  const [officialGifts, setOfficialGifts] = useState<OfficialStarGiftRow[]>([]);
  const [officialQuery, setOfficialQuery] = useState("");
  const [officialCategory, setOfficialCategory] = useState<OfficialGiftCategory>("all");
  const [sourceGiftID, setSourceGiftID] = useState("");
  const [includeCollectible, setIncludeCollectible] = useState(true);
  const [upgradeStars, setUpgradeStars] = useState("0");
  const [supplyTotal, setSupplyTotal] = useState("0");
  const [slugPrefix, setSlugPrefix] = useState("");
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
  const [bulkImportOpen, setBulkImportOpen] = useState<"default" | "official" | null>(null);
  const [bulkImportItems, setBulkImportItems] = useState<Array<DefaultGiftRow | OfficialStarGiftRow>>([]);
  const [bulkImportEnabled, setBulkImportEnabled] = useState(true);
  const [bulkImportReason, setBulkImportReason] = useState("");
  const [bulkImportBusy, setBulkImportBusy] = useState(false);
  const [bulkImportProgress, setBulkImportProgress] = useState({ done: 0, total: 0 });
  const [bulkImportError, setBulkImportError] = useState("");
  const [bulkImportResult, setBulkImportResult] = useState<{ imported: number; skipped: number; failed: number; errors: string[] } | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [bulkReason, setBulkReason] = useState("");
  const [bulkBusy, setBulkBusy] = useState(false);
  const [bulkError, setBulkError] = useState("");
  const [pageSize, setPageSize] = useState<GiftPageSize>(10);
  const [page, setPage] = useState(1);

  async function load() {
    setError("");
    try {
      setGifts((await api.gifts()).Gifts ?? []);
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  useEffect(() => { void load(); }, []);

  useEffect(() => {
    if (!importOpen || importSource !== "default" || defaultGifts.length > 0) return;
    api.defaultGifts().then((value) => setDefaultGifts(value.gifts ?? [])).catch((err) => setImportError(errorMessage(err)));
  }, [importOpen, importSource, defaultGifts.length]);

  useEffect(() => {
    if (!importOpen || importSource !== "official" || officialGifts.length > 0) return;
    api.officialGifts().then((value) => setOfficialGifts(value.gifts ?? [])).catch((err) => setImportError(errorMessage(err)));
  }, [importOpen, importSource, officialGifts.length]);

  const selectedDefault = useMemo(() => defaultGifts.find((gift) => gift.id === selectedDefaultID) ?? null, [defaultGifts, selectedDefaultID]);
  const selectedOfficial = useMemo(() => officialGifts.find((gift) => gift.source_gift_id === sourceGiftID) ?? null, [officialGifts, sourceGiftID]);
  const officialCategoryCounts = useMemo(() => ({
    all: officialGifts.length,
    upgrade: officialGifts.filter((gift) => gift.can_upgrade).length,
    craft: officialGifts.filter((gift) => gift.can_craft).length,
    basic: officialGifts.filter((gift) => !gift.can_upgrade).length
  }), [officialGifts]);
  const visibleOfficial = useMemo(() => {
    const normalized = officialQuery.trim().toLowerCase();
    return officialGifts.filter((gift) => {
      const categoryMatches = officialCategory === "all" ||
        (officialCategory === "upgrade" && gift.can_upgrade) ||
        (officialCategory === "craft" && gift.can_craft) ||
        (officialCategory === "basic" && !gift.can_upgrade);
      return categoryMatches && (!normalized || gift.source_gift_id.includes(normalized) || gift.title.toLowerCase().includes(normalized));
    });
  }, [officialGifts, officialQuery, officialCategory]);

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

  function toggleSelected(giftID: string) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(giftID)) next.delete(giftID);
      else next.add(giftID);
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
      setBulkError(t("action.reasonRequired"));
      return;
    }
    setBulkBusy(true);
    setBulkError("");
    const ids = Array.from(selected);
    let failed = 0;
    for (const id of ids) {
      try {
        await api.action("/api/actions/set-gift-enabled", {
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
      setBulkError(t("gifts.bulkStatusFailed", { failed, total: ids.length }));
    } else {
      setSelected(new Set());
      setBulkReason("");
    }
    await load();
  }

  function uploadForm(confirm: boolean, commandID = "") {
    if (!file) throw new Error(t("gifts.fileRequired"));
    if (!reason.trim()) throw new Error(t("action.reasonRequired"));
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

  function defaultPayload(confirm: boolean, commandID = "") {
    if (!selectedDefaultID) throw new Error(t("gifts.defaultRequired"));
    if (!reason.trim()) throw new Error(t("action.reasonRequired"));
    return { command_id: commandID, reason: reason.trim(), confirm, id: selectedDefaultID };
  }

  function officialPayload(confirm: boolean, commandID = "") {
    if (!sourceGiftID) throw new Error(t("gifts.officialRequired"));
    if (!reason.trim()) throw new Error(t("action.reasonRequired"));
    return {
      command_id: commandID, reason: reason.trim(), confirm,
		source_gift_id: sourceGiftID, gift_id: giftID, title: title.trim(),
		stars, convert_stars: convertStars, enabled, sort_order: Number(sortOrder),
		include_collectible: includeCollectible, upgrade_stars: upgradeStars,
      supply_total: Number(supplyTotal), slug_prefix: slugPrefix.trim().toLowerCase()
    };
  }

  function chooseOfficial(gift: OfficialStarGiftRow) {
    setSourceGiftID(gift.source_gift_id);
    setTitle(gift.title || t("gifts.officialUnnamed", { id: gift.source_gift_id }));
    setStars(String(gift.stars));
    setConvertStars(String(gift.convert_stars));
    setIncludeCollectible(gift.can_upgrade);
		setUpgradeStars(gift.upgrade_stars);
    setSupplyTotal(String(gift.availability_total || 1));
    setSlugPrefix(`official-${gift.source_gift_id}`);
    setPreview(null);
  }

  async function openBulkImport(source: "default" | "official") {
    setBulkImportOpen(source);
    setBulkImportItems([]);
    setBulkImportEnabled(true);
    setBulkImportReason("");
    setBulkImportBusy(false);
    setBulkImportProgress({ done: 0, total: 0 });
    setBulkImportError("");
    setBulkImportResult(null);
    try {
      if (source === "default") {
        const list = defaultGifts.length > 0 ? defaultGifts : (await api.defaultGifts()).gifts ?? [];
        setBulkImportItems(list);
      } else {
        const list = officialGifts.length > 0 ? officialGifts : (await api.officialGifts()).gifts ?? [];
        setBulkImportItems(list);
      }
    } catch (err) {
      setBulkImportError(errorMessage(err));
    }
  }

  function closeBulkImport() {
    if (bulkImportBusy) return;
    setBulkImportOpen(null);
  }

  async function runBulkImport() {
    if (!bulkImportOpen) return;
    if (!bulkImportReason.trim()) { setBulkImportError(t("action.reasonRequired")); return; }
    const source = bulkImportOpen;
    setBulkImportBusy(true); setBulkImportError(""); setBulkImportResult(null);
    setBulkImportProgress({ done: 0, total: bulkImportItems.length });
    let imported = 0, skipped = 0, failed = 0;
    const errors: string[] = [];
    for (const item of bulkImportItems) {
      const label = source === "default" ? (item as DefaultGiftRow).title : ((item as OfficialStarGiftRow).title || `#${(item as OfficialStarGiftRow).source_gift_id}`);
      try {
        // Stable per-gift command_id (mirrors the old server-side bulk
        // endpoint) so a gift already imported by a prior run is recognized
        // as a replay instead of creating a duplicate catalog entry -
        // CreateCatalogBundle has no unique constraint on title/source id to
        // fall back on. If this run's Enabled value differs from the run
        // that first created it, the server reports COMMAND_ID_CONFLICT
        // instead of silently re-importing; treat that as "skipped" too.
        const result = source === "default"
          ? await api.importDefaultGift({
              command_id: `bulk-default-gift-${(item as DefaultGiftRow).id}`,
              reason: bulkImportReason.trim(),
              confirm: true,
              id: (item as DefaultGiftRow).id,
              enabled: bulkImportEnabled
            })
          : await api.importOfficialGift({
              command_id: `bulk-official-gift-${(item as OfficialStarGiftRow).source_gift_id}`,
              reason: bulkImportReason.trim(),
              confirm: true,
              source_gift_id: (item as OfficialStarGiftRow).source_gift_id,
              include_collectible: (item as OfficialStarGiftRow).can_upgrade,
              enabled: bulkImportEnabled
            });
        if (result.already_executed || result.details?.skipped) skipped++;
        else imported++;
      } catch (err) {
        if (err instanceof APIError && err.message === "COMMAND_ID_CONFLICT") {
          skipped++;
        } else {
          failed++;
          errors.push(`${label}: ${errorMessage(err)}`);
        }
      }
      setBulkImportProgress((prev) => ({ ...prev, done: prev.done + 1 }));
    }
    setBulkImportBusy(false);
    setBulkImportResult({ imported, skipped, failed, errors });
    await load();
  }

  async function validateImport() {
    setBusy(true); setImportError(""); setPreview(null);
    try {
      const result = importSource === "default" ? await api.importDefaultGift(defaultPayload(false))
        : importSource === "official" ? await api.importOfficialGift(officialPayload(false))
        : await api.importGift(uploadForm(false));
      setPreview(result);
    } catch (err) {
      setImportError(errorMessage(err));
    } finally { setBusy(false); }
  }

  async function confirmImport() {
    if (!preview) return;
    setBusy(true); setImportError("");
    try {
      if (importSource === "default") await api.importDefaultGift(defaultPayload(true, preview.command_id));
      else if (importSource === "official") await api.importOfficialGift(officialPayload(true, preview.command_id));
      else await api.importGift(uploadForm(true, preview.command_id));
			setPreview(null); setFile(null); setGiftID("0"); setTitle(""); setSelectedDefaultID(0); setSourceGiftID("");
      await load();
      setImportOpen(false);
    } catch (err) {
      setImportError(errorMessage(err));
    } finally { setBusy(false); }
  }

  function startImport() {
	setGiftID("0"); setTitle(""); setStars("50"); setConvertStars("50"); setSortOrder("0");
    setEnabled(true); setReason(""); setFile(null); setPreview(null); setImportError("");
    setImportSource(SHOW_DEFAULT_GIFTS_TAB ? "default" : "official"); setSelectedDefaultID(0);
    setSourceGiftID(""); setOfficialQuery(""); setOfficialCategory("all");
    setBulkImportBusy(false); setBulkImportProgress({ done: 0, total: 0 }); setBulkImportError("");
    setImportOpen(true);
  }

  function startRevision(gift: StarGiftRow) {
    setGiftID(gift.GiftID); setTitle(gift.Title); setStars(String(gift.Stars));
    setConvertStars(String(gift.ConvertStars)); setSortOrder(String(gift.SortOrder)); setEnabled(gift.Enabled);
    setReason(""); setFile(null); setPreview(null); setImportError("");
    setImportSource("file"); setSelectedDefaultID(0); setSourceGiftID(""); setImportOpen(true);
  }

  const step1Done = importSource === "default" ? selectedDefaultID > 0
    : importSource === "official" ? Boolean(sourceGiftID)
    : Boolean(file);

  return (
    <PageFrame title={t("gifts.pageTitle")} eyebrow={t("gifts.eyebrow")} actions={<>
      <button className="btn" type="button" onClick={() => load()} disabled={busy}><RefreshCw size={15} /> {t("common.refresh")}</button>
      <button className="btn primary" type="button" onClick={startImport}><Plus size={15} /> {t("gifts.add")}</button>
    </>}>
      {error && <Alert>{error}</Alert>}
      <div className="metric-row gift-metrics">
        <Metric label={t("gifts.total")} value={String(gifts.length)} />
        <Metric label={t("gifts.enabled")} value={String(gifts.filter((gift) => gift.Enabled).length)} tone="good" />
		<Metric label={t("gifts.received")} value={gifts.reduce((sum, gift) => sum + BigInt(gift.ReceivedCount), 0n).toString()} />
        <Metric label={t("gifts.formats")} value="TGS / Lottie" />
      </div>
      <QueryPanel>
        <div className="toolbar">
          <label className="searchbox"><Search size={15} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("gifts.searchPlaceholder")} /></label>
          <label className="gift-page-size"><span>{t("gifts.perPage")}</span>
            <select value={String(pageSize)} onChange={(event) => setPageSize(event.target.value === "all" ? "all" : (Number(event.target.value) as GiftPageSize))}>
              <option value="10">10</option>
              <option value="20">20</option>
              <option value="50">50</option>
              <option value="100">100</option>
              <option value="all">{t("gifts.perPageAll")}</option>
            </select>
          </label>
          <span className="gift-list-summary">{t("gifts.listSummary", { shown: visibleGifts.length, total: gifts.length })}</span>
        </div>
      </QueryPanel>
      {selected.size > 0 && <div className="gift-bulk-toolbar">
        <span className="gift-bulk-count">{t("gifts.bulkSelected", { count: selected.size })}</span>
        <label className="gift-reason-field gift-bulk-reason"><span>{t("gifts.reason")}</span><input value={bulkReason} placeholder={t("gifts.reasonPlaceholder")} onChange={(e) => setBulkReason(e.target.value)} /></label>
        <button className="btn" type="button" onClick={() => bulkSetEnabled(true)} disabled={bulkBusy}>
          {bulkBusy ? <Loader2 className="spin" size={14} /> : <CheckCircle2 size={14} />} {t("gifts.bulkEnable")}
        </button>
        <button className="btn" type="button" onClick={() => bulkSetEnabled(false)} disabled={bulkBusy}>
          {bulkBusy ? <Loader2 className="spin" size={14} /> : <Pause size={14} />} {t("gifts.bulkDisable")}
        </button>
        <button className="btn" type="button" onClick={() => { setSelected(new Set()); setBulkError(""); }} disabled={bulkBusy}>{t("common.close")}</button>
        {bulkError && <span className="gift-bulk-error">{bulkError}</span>}
      </div>}
      <div className="table-wrap gift-table-wrap">
        <table className="data-table gift-table">
          <thead><tr><th className="gift-select-col"><input type="checkbox" checked={allVisibleSelected} onChange={toggleSelectAllVisible} aria-label={t("gifts.bulkSelectAll")} /></th><th>{t("gifts.animation")}</th><th>{t("gifts.idRevision")}</th><th>{t("gifts.title")}</th><th>{t("gifts.price")}</th><th>{t("gifts.source")}</th><th>{t("gifts.received")}</th><th>{t("common.status")}</th><th>{t("common.updatedAt")}</th><th>{t("common.actions")}</th></tr></thead>
          <tbody>
            {pagedGifts.map((gift) => (
              <tr className={gift.Enabled ? "" : "gift-row-disabled"} key={gift.GiftID}>
                <td className="gift-select-col"><input type="checkbox" checked={selected.has(gift.GiftID)} onChange={() => toggleSelected(gift.GiftID)} aria-label={t("gifts.bulkSelectOne", { id: gift.GiftID })} /></td>
                <td><LottiePreview giftID={gift.GiftID} revision={gift.Revision} compact /></td>
                <td className="mono">{gift.GiftID} / {gift.Revision}</td>
                <td><strong className="gift-table-title">{gift.Title || `Gift #${gift.GiftID}`}</strong><span className="gift-sort-order">{t("gifts.sortOrder")}: {gift.SortOrder}</span></td>
                <td><strong className="gift-table-price">⭐ {gift.Stars}</strong><span className="gift-convert-price">→ {gift.ConvertStars}</span></td>
                <td><Badge>{gift.SourceFormat}</Badge><span className="gift-source-size">{formatBytes(gift.AnimationSize)}</span></td>
                <td>{gift.ReceivedCount}</td>
                <td><Badge tone={gift.Enabled ? "good" : "neutral"}>{gift.Enabled ? t("common.enabled") : t("common.disabled")}</Badge></td>
                <td>{formatDate(gift.UpdatedAt)}</td>
                <td><div className="gift-table-actions"><button className="btn compact-btn collectible-button" type="button" onClick={() => setCollectibleGift(gift)}><Gem size={13} />{t("collectibles.manage")}</button><button className="btn compact-btn" type="button" onClick={() => startRevision(gift)}>{t("gifts.replace")}</button><ActionButton compact tone="neutral" label={gift.Enabled ? t("gifts.disable") : t("gifts.enable")} path="/api/actions/set-gift-enabled" payload={() => ({ gift_id: gift.GiftID, enabled: !gift.Enabled })} onDone={() => void load()} /></div></td>
              </tr>
            ))}
            {pagedGifts.length === 0 && <EmptyRow colSpan={10} />}
          </tbody>
        </table>
      </div>
      {pageSize !== "all" && visibleGifts.length > 0 && <div className="gift-pager">
        <span className="gift-pager-range">{t("gifts.pageRange", { start: pageRangeStart, end: pageRangeEnd, total: visibleGifts.length })}</span>
        <div className="gift-pager-controls">
          <button className="btn compact-btn" type="button" onClick={() => setPage((p) => Math.max(1, p - 1))} disabled={currentPage <= 1}>
            <ChevronLeft size={14} /> {t("gifts.pagePrev")}
          </button>
          <span className="gift-pager-page">{t("gifts.pageOf", { page: currentPage, total: totalPages })}</span>
          <button className="btn compact-btn" type="button" onClick={() => setPage((p) => Math.min(totalPages, p + 1))} disabled={currentPage >= totalPages}>
            {t("gifts.pageNext")} <ChevronRight size={14} />
          </button>
        </div>
      </div>}

      {importOpen && createPortal(
        <div className="modal-backdrop" role="presentation">
			<section className="modal command-modal gift-import-modal" role="dialog" aria-modal="true" aria-label={giftID !== "0" ? t("gifts.newRevision", { id: giftID }) : t("gifts.importTitle")}>
            <div className="modal-head">
				<div><div className="eyebrow">{t("gifts.importEyebrow")}</div><h2>{giftID !== "0" ? t("gifts.newRevision", { id: giftID }) : t("gifts.importTitle")}</h2></div>
              <button className="icon-btn" type="button" onClick={() => setImportOpen(false)} disabled={busy} aria-label={t("action.close")}><X size={15} /></button>
            </div>
            <div className="command-body gift-import-modal-body">
              <div className="command-steps">
                <div className={`command-step ${step1Done ? "done" : "active"}`}><span>1</span><strong>{t("gifts.stepDetails")}</strong></div>
                <div className={`command-step ${preview ? "done" : step1Done ? "active" : ""}`}><span>2</span><strong>{t("gifts.stepValidate")}</strong></div>
                <div className={`command-step ${preview ? "active" : ""}`}><span>3</span><strong>{t("gifts.stepImport")}</strong></div>
              </div>
              {giftID === "0" && <div className="gift-source-tabs">
                {SHOW_DEFAULT_GIFTS_TAB && <button className={`btn ${importSource === "default" ? "primary" : ""}`} type="button" onClick={() => { setImportSource("default"); setPreview(null); }}>{t("gifts.defaultSource")}</button>}
                <button className={`btn ${importSource === "official" ? "primary" : ""}`} type="button" onClick={() => { setImportSource("official"); setPreview(null); }}>{t("gifts.officialSource")}</button>
                <button className={`btn ${importSource === "file" ? "primary" : ""}`} type="button" onClick={() => { setImportSource("file"); setPreview(null); }}>{t("gifts.fileSource")}</button>
              </div>}
              {importSource === "default" && giftID === "0" && SHOW_DEFAULT_GIFTS_TAB ? <section className="official-gift-picker">
                <div className="gift-import-note"><span>{t("gifts.defaultHint")}</span><div className="gift-format-chips"><span>{defaultGifts.length}</span><span>OwpenGram</span></div></div>
                <div className="official-gift-bulk-import">
                  <button className="btn" type="button" onClick={() => openBulkImport("default")}>
                    <Upload size={14} /> {t("gifts.importAllDefault")}
                  </button>
                </div>
                <label className="gift-switch"><input type="checkbox" checked={enabled} onChange={(e) => { setEnabled(e.target.checked); setPreview(null); }} /><span className="gift-switch-track" aria-hidden="true"><span /></span><span>{t("gifts.enableAfterImport")}</span></label>
                <div className="official-gift-list" role="listbox" aria-label={t("gifts.defaultSelect")}>
                  {defaultGifts.map((gift) => {
                    const isSelected = gift.id === selectedDefaultID;
                    return <button key={gift.id} className={`official-gift-option ${isSelected ? "selected" : ""}`}
                      type="button" role="option" aria-selected={isSelected} onClick={() => { setSelectedDefaultID(gift.id); setPreview(null); }}>
                      <span className="official-gift-option-head">
                        <strong>{gift.title}</strong>
                        <span className="mono">⭐ {gift.stars}</span>
                      </span>
                      <span className="official-gift-option-meta">
                        <span>{t("gifts.officialAttributes", { count: defaultGiftAttributeCount(gift) })}</span>
                        {gift.limited && <span>{t("gifts.limited", { total: gift.availability })}</span>}
                        {gift.require_premium && <span>{t("gifts.premium")}</span>}
                      </span>
                      <span className="official-gift-capabilities">
                        <span className={gift.upgradeable ? "yes" : "no"}>{gift.upgradeable ? t("gifts.canUpgrade") : t("gifts.cannotUpgrade")}</span>
                        <span className={gift.craftable ? "craft" : "no"}>{gift.craftable ? t("gifts.canCraft") : t("gifts.cannotCraft")}</span>
                      </span>
                    </button>;
                  })}
                  {defaultGifts.length === 0 && <div className="official-gift-empty">{t("gifts.defaultEmpty")}</div>}
                </div>
                {selectedDefault && <div className="official-gift-selected">
                  <DefaultLottiePreview id={selectedDefault.id} />
                  <div><strong>{selectedDefault.title}</strong><span className="mono">⭐ {selectedDefault.stars} → {selectedDefault.convert_stars}</span><small>{selectedDefault.model_count} {t("collectibles.models")} · {selectedDefault.pattern_count} {t("collectibles.patterns")} · {selectedDefault.backdrop_count} {t("collectibles.backdrops")}</small><span className="official-gift-capabilities"><span className={selectedDefault.upgradeable ? "yes" : "no"}>{selectedDefault.upgradeable ? t("gifts.canUpgrade") : t("gifts.cannotUpgrade")}</span><span className={selectedDefault.craftable ? "craft" : "no"}>{selectedDefault.craftable ? t("gifts.canCraft") : t("gifts.cannotCraft")}</span></span></div>
                </div>}
              </section> : importSource === "official" && giftID === "0" ? <section className="official-gift-picker">
                <div className="gift-import-note"><span>{t("gifts.officialHint")}</span><div className="gift-format-chips"><span>{officialGifts.length}</span><span>SHA-256</span></div></div>
                <div className="official-gift-bulk-import">
                  <button className="btn" type="button" onClick={() => openBulkImport("official")}>
                    <Upload size={14} /> {t("gifts.importAllOfficial")}
                  </button>
                </div>
                <div className="official-gift-tools">
                  <label className="searchbox"><Search size={15} /><input value={officialQuery} onChange={(e) => setOfficialQuery(e.target.value)} placeholder={t("gifts.officialSearch")} /></label>
                  <span>{t("gifts.officialResults", { shown: visibleOfficial.length, total: officialGifts.length })}</span>
                </div>
                <div className="official-gift-categories" role="group" aria-label={t("gifts.officialCategoryLabel")}>
                  {(["all", "upgrade", "craft", "basic"] as const).map((category) => (
                    <button key={category} className={officialCategory === category ? "active" : ""} type="button"
                      aria-pressed={officialCategory === category} onClick={() => setOfficialCategory(category)}>
                      {t(`gifts.officialCategory.${category}`)}<span>{officialCategoryCounts[category]}</span>
                    </button>
                  ))}
                </div>
                <div className="official-gift-list" role="listbox" aria-label={t("gifts.officialSelect")}>
                  {visibleOfficial.map((gift) => {
                    const isSelected = gift.source_gift_id === sourceGiftID;
                    return <button key={gift.source_gift_id} className={`official-gift-option ${isSelected ? "selected" : ""}`}
                      type="button" role="option" aria-selected={isSelected} onClick={() => chooseOfficial(gift)}>
                      <span className="official-gift-option-head">
                        <strong>{gift.title || t("gifts.officialUnnamed", { id: gift.source_gift_id })}</strong>
                        <span className="mono">#{gift.source_gift_id}</span>
                      </span>
                      <span className="official-gift-option-meta">
                        <span>⭐ {gift.stars}</span>
                        <span>{t("gifts.officialAttributes", { count: officialGiftAttributeCount(gift) })}</span>
                      </span>
                      <span className="official-gift-capabilities">
                        <span className={gift.can_upgrade ? "yes" : "no"}>{gift.can_upgrade ? t("gifts.canUpgrade") : t("gifts.cannotUpgrade")}</span>
                        <span className={gift.can_craft ? "craft" : "no"}>{gift.can_craft ? t("gifts.canCraft") : t("gifts.cannotCraft")}</span>
                      </span>
                    </button>;
                  })}
                  {visibleOfficial.length === 0 && <div className="official-gift-empty">{t("gifts.officialEmpty")}</div>}
                </div>
                {selectedOfficial && <div className="official-gift-selected">
                  <OfficialLottiePreview sourceGiftID={selectedOfficial.source_gift_id} />
                  <div><strong>{selectedOfficial.title || t("gifts.officialUnnamed", { id: selectedOfficial.source_gift_id })}</strong><span className="mono">{selectedOfficial.source_gift_id}</span><small>{selectedOfficial.model_count} {t("collectibles.models")} · {selectedOfficial.pattern_count} {t("collectibles.patterns")} · {selectedOfficial.backdrop_count} {t("collectibles.backdrops")}</small><span className="official-gift-capabilities"><span className={selectedOfficial.can_upgrade ? "yes" : "no"}>{selectedOfficial.can_upgrade ? t("gifts.canUpgrade") : t("gifts.cannotUpgrade")}</span><span className={selectedOfficial.can_craft ? "craft" : "no"}>{selectedOfficial.can_craft ? t("gifts.canCraft") : t("gifts.cannotCraft")}</span></span></div>
                </div>}
                {selectedOfficial?.can_upgrade && <>
                  <label className="gift-switch"><input type="checkbox" checked={includeCollectible} onChange={(e) => { setIncludeCollectible(e.target.checked); setPreview(null); }} /><span className="gift-switch-track" aria-hidden="true"><span /></span><span>{t("gifts.includeCollectible")}</span></label>
                  {includeCollectible && <div className="gift-fields-grid">
                    <label><span>{t("collectibles.upgradeStars")}</span><input type="number" min="1" value={upgradeStars} onChange={(e) => { setUpgradeStars(e.target.value); setPreview(null); }} /></label>
                    <label><span>{t("collectibles.supply")}</span><input type="number" min="1" value={supplyTotal} onChange={(e) => { setSupplyTotal(e.target.value); setPreview(null); }} /></label>
                    <label><span>{t("collectibles.slug")}</span><input value={slugPrefix} maxLength={48} onChange={(e) => { setSlugPrefix(e.target.value.toLowerCase()); setPreview(null); }} /></label>
                  </div>}
                </>}
                <div className="gift-fields-grid">
                  <label><span>{t("gifts.title")}</span><input value={title} maxLength={128} placeholder={t("gifts.titlePlaceholder")} onChange={(e) => { setTitle(e.target.value); setPreview(null); }} /></label>
                  <label><span>{t("gifts.stars")}</span><input type="number" min="1" value={stars} onChange={(e) => { setStars(e.target.value); setPreview(null); }} /></label>
                  <label><span>{t("gifts.convertStars")}</span><input type="number" min="0" value={convertStars} onChange={(e) => { setConvertStars(e.target.value); setPreview(null); }} /></label>
                  <label><span>{t("gifts.sortOrder")}</span><input type="number" value={sortOrder} onChange={(e) => { setSortOrder(e.target.value); setPreview(null); }} /></label>
                </div>
                <label className="gift-switch"><input type="checkbox" checked={enabled} onChange={(e) => { setEnabled(e.target.checked); setPreview(null); }} /><span className="gift-switch-track" aria-hidden="true"><span /></span><span>{t("gifts.enableAfterImport")}</span></label>
              </section> : <>
                <div className="gift-import-note"><span>{t("gifts.importHint")}</span><div className="gift-format-chips" aria-label={t("gifts.formats")}><span>TGS</span><span>Lottie JSON</span></div></div>
                <label className={`gift-file-picker ${file ? "has-file" : ""}`}>
                  <input type="file" accept=".tgs,.json,.lottie,application/json,application/x-tgsticker" onChange={(e) => { setFile(e.target.files?.[0] ?? null); setPreview(null); }} />
                  <span className="gift-file-icon"><FileJson2 size={22} /></span>
                  <span className="gift-file-copy"><span className="gift-field-label">{t("gifts.animation")}</span><strong>{file ? file.name : t("gifts.filePrompt")}</strong><small>{file ? formatBytes(file.size) : t("gifts.fileHint")}</small></span>
                  <span className="gift-file-action">{file ? t("gifts.changeFile") : t("gifts.chooseFile")}</span>
                </label>
                <div className="gift-fields-grid">
                  <label><span>{t("gifts.title")}</span><input value={title} maxLength={128} placeholder={t("gifts.titlePlaceholder")} onChange={(e) => { setTitle(e.target.value); setPreview(null); }} /></label>
                  <label><span>{t("gifts.stars")}</span><input type="number" min="1" value={stars} onChange={(e) => { setStars(e.target.value); setPreview(null); }} /></label>
                  <label><span>{t("gifts.convertStars")}</span><input type="number" min="0" value={convertStars} onChange={(e) => { setConvertStars(e.target.value); setPreview(null); }} /></label>
                  <label><span>{t("gifts.sortOrder")}</span><input type="number" value={sortOrder} onChange={(e) => { setSortOrder(e.target.value); setPreview(null); }} /></label>
                </div>
                <label className="gift-switch"><input type="checkbox" checked={enabled} onChange={(e) => { setEnabled(e.target.checked); setPreview(null); }} /><span className="gift-switch-track" aria-hidden="true"><span /></span><span>{t("gifts.enableAfterImport")}</span></label>
              </>}
              <label className="gift-reason-field"><span>{t("gifts.reason")}</span><input value={reason} placeholder={t("gifts.reasonPlaceholder")} onChange={(e) => setReason(e.target.value)} /></label>
              {importError && <Alert>{importError}</Alert>}
              {preview && <div className="gift-validation"><div className="gift-validation-head"><CheckCircle2 size={17} /><div><strong>{t("gifts.validationReady")}</strong><span>{t("gifts.validationHint")}</span></div></div><pre>{JSON.stringify(preview.details, null, 2)}</pre></div>}
            </div>
            <div className="modal-actions">
              <button className="btn" type="button" onClick={() => setImportOpen(false)} disabled={busy}>{t("common.close")}</button>
              <button className="btn" type="button" onClick={validateImport} disabled={busy}>{busy ? <Loader2 className="spin" size={15} /> : <ShieldCheck size={15} />}{t("gifts.validate")}</button>
              <button className="btn primary" type="button" onClick={confirmImport} disabled={busy || !preview}><Upload size={15} />{t("gifts.confirmImport")}</button>
            </div>
          </section>
        </div>,
        document.body
      )}
      {bulkImportOpen && createPortal(
        <div className="modal-backdrop" role="presentation">
          <section className="modal command-modal gift-bulk-import-modal" role="dialog" aria-modal="true"
            aria-label={bulkImportOpen === "default" ? t("gifts.importAllDefault") : t("gifts.importAllOfficial")}>
            <div className="modal-head">
              <div><div className="eyebrow">{t("gifts.importEyebrow")}</div><h2>{bulkImportOpen === "default" ? t("gifts.importAllDefault") : t("gifts.importAllOfficial")}</h2></div>
              <button className="icon-btn" type="button" onClick={closeBulkImport} disabled={bulkImportBusy} aria-label={t("action.close")}><X size={15} /></button>
            </div>
            <div className="command-body">
              <div className="gift-import-note"><span>{t("gifts.bulkImportCount", { count: bulkImportItems.length })}</span></div>
              <label className="gift-switch"><input type="checkbox" checked={bulkImportEnabled} disabled={bulkImportBusy} onChange={(e) => setBulkImportEnabled(e.target.checked)} /><span className="gift-switch-track" aria-hidden="true"><span /></span><span>{t("gifts.enableAfterImport")}</span></label>
              <label className="gift-reason-field"><span>{t("gifts.reason")}</span><input value={bulkImportReason} placeholder={t("gifts.reasonPlaceholder")} disabled={bulkImportBusy} onChange={(e) => setBulkImportReason(e.target.value)} /></label>
              {bulkImportBusy && <div className="gift-bulk-import-progress">
                <div className="gift-bulk-import-progress-bar"><div style={{ width: `${bulkImportProgress.total ? Math.round((bulkImportProgress.done / bulkImportProgress.total) * 100) : 0}%` }} /></div>
                <span>{t("gifts.importingProgress", { done: bulkImportProgress.done, total: bulkImportProgress.total })}</span>
              </div>}
              {bulkImportError && <Alert>{bulkImportError}</Alert>}
              {bulkImportResult && <div className="gift-validation">
                <div className="gift-validation-head"><CheckCircle2 size={17} /><div><strong>{t("gifts.bulkImportDone")}</strong><span>{t("gifts.bulkImportSummary", { imported: bulkImportResult.imported, skipped: bulkImportResult.skipped, failed: bulkImportResult.failed })}</span></div></div>
                {bulkImportResult.errors.length > 0 && <pre>{bulkImportResult.errors.join("\n")}</pre>}
              </div>}
            </div>
            <div className="modal-actions">
              <button className="btn" type="button" onClick={closeBulkImport} disabled={bulkImportBusy}>{t("common.close")}</button>
              <button className="btn primary" type="button" onClick={runBulkImport} disabled={bulkImportBusy || bulkImportItems.length === 0}>
                {bulkImportBusy ? <Loader2 className="spin" size={15} /> : <Upload size={15} />} {t("gifts.startBulkImport")}
              </button>
            </div>
          </section>
        </div>,
        document.body
      )}
      {collectibleGift && <GiftCollectiblesModal gift={collectibleGift} onClose={() => setCollectibleGift(null)} onPublished={() => void load()} />}
    </PageFrame>
  );
}
