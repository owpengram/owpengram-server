import { ArrowsRotateRight, ChevronLeft, ChevronRight, Eye, Magnifier, Picture, Plus } from "@gravity-ui/icons";
import { useEffect, useMemo, useState } from "react";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { StickerDocumentPreview } from "../components/StickerDocumentPreview";
import { DataTable, FilterChip, type Column } from "../components/DataTable";
import { Alert, Badge, PageFrame } from "../components/ui";
import type { StickerSetRow } from "../types";
import { CreateStickerSetModal } from "./CreateStickerSetModal";
import { StickerSetPreviewModal } from "./StickerSetPreviewModal";

type StickerPageSize = 20 | 50 | 100 | "all";
type SetFilter = "official" | "archived";

// Shared list/manage view for one non-system sticker-set kind ("stickers" or
// "emoji") — system packs (dice, animated emoji, gifts) aren't shown here,
// they're not hand-edited.
export function StickerSetsPage({ kind }: { kind: "stickers" | "emoji" }) {
  const [sets, setSets] = useState<StickerSetRow[]>([]);
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [pageSize, setPageSize] = useState<StickerPageSize>(50);
  const [filter, setFilter] = useState<SetFilter | null>(null);
  const [page, setPage] = useState(1);
  const [orderDrafts, setOrderDrafts] = useState<Record<string, string>>({});
  const [titleDrafts, setTitleDrafts] = useState<Record<string, string>>({});
  const [previewSet, setPreviewSet] = useState<StickerSetRow | null>(null);
  const [createOpen, setCreateOpen] = useState(false);

  const pageTitle = kind === "emoji" ? "Emoji" : "Stickers";
  const noun = kind === "emoji" ? "emoji" : "sticker";

  async function load() {
    setBusy(true);
    setError("");
    try {
      setSets((await api.stickerSets(kind)).rows ?? []);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => { void load(); }, [kind]);

  const visible = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    const byFilter = sets.filter((set) => (filter === "official" ? set.Official : filter === "archived" ? set.Archived : true));
    if (!normalized) return byFilter;
    return byFilter.filter((set) =>
      String(set.ID).includes(normalized) ||
      set.ShortName.toLowerCase().includes(normalized) ||
      set.Title.toLowerCase().includes(normalized)
    );
  }, [sets, query, filter]);

  useEffect(() => { setPage(1); }, [query, pageSize, kind, filter]);

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
    total: sets.length,
    official: sets.filter((set) => set.Official).length,
    archived: sets.filter((set) => set.Archived).length
  }), [sets]);

  const columns: Column<StickerSetRow>[] = [
    {
      key: "title",
      label: "Pack",
      sortValue: (set) => set.Title,
      render: (set) => (
        <span className="cell-identity">
          {set.CoverDocumentID ? (
            <StickerDocumentPreview documentID={set.CoverDocumentID} className="list-thumb" showError={false} />
          ) : (
            <span className="sticker-list-thumb-empty"><Picture width={14} height={14} /></span>
          )}
          <span className="sort-order-editor">
            <input
              className="small-input title-input"
              value={titleDrafts[set.ID] ?? set.Title}
              onChange={(event) => setTitleDrafts((prev) => ({ ...prev, [set.ID]: event.target.value }))}
              aria-label={"Title"}
            />
            {(titleDrafts[set.ID] ?? set.Title) !== set.Title && (
              <ActionButton
                compact
                tone="neutral"
                label={"Save"}
                path="/api/actions/rename-sticker-set"
                payload={() => ({ set_id: set.ID, title: (titleDrafts[set.ID] ?? set.Title).trim() })}
                onDone={() => void load()}
              />
            )}
          </span>
        </span>
      )
    },
    { key: "short", label: "Short name", className: "mono", sortValue: (set) => set.ShortName, render: (set) => set.ShortName || <span className="muted-cell">—</span> },
    { key: "count", label: noun === "emoji" ? "Emoji" : "Stickers", align: "right", firstDir: "desc", sortValue: (set) => set.Count, render: (set) => set.Count },
    {
      key: "order",
      label: "Order",
      sortValue: (set) => set.SortOrder,
      render: (set) => (
        <span className="sort-order-editor">
          <input
            type="number"
            className="small-input"
            value={orderDrafts[set.ID] ?? String(set.SortOrder)}
            onChange={(event) => setOrderDrafts((prev) => ({ ...prev, [set.ID]: event.target.value }))}
            aria-label={"Sort order"}
          />
          {(orderDrafts[set.ID] ?? String(set.SortOrder)) !== String(set.SortOrder) && (
            <ActionButton
              compact
              tone="neutral"
              label={"Save"}
              path="/api/actions/set-sticker-set-sort-order"
              payload={() => ({ set_id: set.ID, sort_order: Number(orderDrafts[set.ID] ?? set.SortOrder) })}
              onDone={() => void load()}
            />
          )}
        </span>
      )
    },
    {
      key: "status",
      label: "",
      render: (set) => (
        <span className="cell-badges">
          {set.Official && <Badge tone="good">{"Official"}</Badge>}
          {set.Archived && <Badge tone="danger">{"Archived"}</Badge>}
        </span>
      )
    },
    {
      key: "actions",
      label: "",
      render: (set) => (
        <span className="gift-table-actions">
          <button className="icon-btn" type="button" onClick={() => setPreviewSet(set)} aria-label={"View pack"} title={"View pack"}>
            <Eye />
          </button>
          <ActionButton
            compact
            tone="neutral"
            label={set.Archived ? "Unarchive" : "Archive"}
            path="/api/actions/set-sticker-set-archived"
            payload={() => ({ set_id: set.ID, archived: !set.Archived })}
            onDone={() => void load()}
          />
          <ActionButton
            compact
            tone="danger"
            label={"Delete"}
            path="/api/actions/delete-sticker-set"
            payload={() => ({ set_id: set.ID })}
            onDone={() => void load()}
          />
        </span>
      )
    }
  ];

  return (
    <PageFrame
      title={pageTitle}
      actions={
        <>
          <button className="btn primary icon-text" type="button" onClick={() => setCreateOpen(true)}>
            <Plus /> {`Create ${noun} pack`}
          </button>
          <button className="icon-btn" type="button" aria-label={"Refresh"} title={"Refresh"} onClick={() => void load()} disabled={busy}>
            <ArrowsRotateRight className={busy ? "spin" : undefined} />
          </button>
        </>
      }
    >
      {error && <Alert>{error}</Alert>}
      <div className="list-controls">
        <label className="searchbox">
          <Magnifier />
          <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={"Search by title, short name or ID"} aria-label={"Search packs"} />
        </label>
        <FilterChip label={"Official"} count={counts.official} active={filter === "official"} onToggle={() => setFilter(filter === "official" ? null : "official")} />
        <FilterChip label={"Archived"} count={counts.archived} active={filter === "archived"} onToggle={() => setFilter(filter === "archived" ? null : "archived")} />
      </div>
      <DataTable
        columns={columns}
        rows={paged}
        rowKey={(set) => set.ID}
        onRowClick={(set) => setPreviewSet(set)}
        loading={busy && sets.length === 0}
        empty={query || filter ? "No packs match." : `No ${noun} packs yet. System packs are not listed here.`}
      />
      {visible.length > 0 && (
        <div className="table-footer">
          <span>{`${rangeStart}–${rangeEnd} of ${visible.length}${visible.length !== sets.length ? ` (${sets.length} total)` : ""}`}</span>
          <div className="pager">
            <span>{"Rows"}</span>
            <select
              value={String(pageSize)}
              onChange={(event) => setPageSize(event.target.value === "all" ? "all" : (Number(event.target.value) as StickerPageSize))}
              aria-label={"Rows per page"}
            >
              <option value="20">20</option>
              <option value="50">50</option>
              <option value="100">100</option>
              <option value="all">{"All"}</option>
            </select>
            <button className="icon-btn" type="button" aria-label={"Previous page"} onClick={() => setPage((p) => Math.max(1, p - 1))} disabled={currentPage <= 1}>
              <ChevronLeft />
            </button>
            <button className="icon-btn" type="button" aria-label={"Next page"} onClick={() => setPage((p) => Math.min(totalPages, p + 1))} disabled={currentPage >= totalPages}>
              <ChevronRight />
            </button>
          </div>
        </div>
      )}
      {previewSet && <StickerSetPreviewModal set={previewSet} onClose={() => setPreviewSet(null)} />}
      {createOpen && <CreateStickerSetModal kind={kind} onClose={() => setCreateOpen(false)} onCreated={() => void load()} />}
    </PageFrame>
  );
}
