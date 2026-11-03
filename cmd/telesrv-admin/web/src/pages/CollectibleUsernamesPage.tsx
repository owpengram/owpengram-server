import { ArrowsRotateRight, Magnifier, Plus } from "@gravity-ui/icons";
import { Flame, Vault } from "../components/icons";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import { DataTable, FilterChip, type Column } from "../components/DataTable";
import { MintCollectibleUsernameModal } from "../components/MintCollectibleUsernameModal";
import { Alert, Badge, PageFrame } from "../components/ui";
import { displayUsername, formatCurrency, formatDate, formatRelative } from "../lib/format";
import { useDebounced } from "../lib/useDebounced";
import type { Navigate } from "../routing";
import type { CollectibleUsernameRow, CollectibleUsernameStatus } from "../types";

type StatusFilter = "all" | CollectibleUsernameStatus;

const STATUS_VIEWS: { value: StatusFilter; label: string }[] = [
  { value: "all", label: "All" },
  { value: "owned", label: "Owned" },
  { value: "vault", label: "In vault" },
  { value: "burned", label: "Burned" }
];

export function CollectibleUsernamesPage({ navigate }: { navigate: Navigate }) {
  const [status, setStatus] = useState<StatusFilter>("all");
  const [q, setQ] = useState("");
  const query = useDebounced(q.trim().replace(/^@/, ""));
  const [rows, setRows] = useState<CollectibleUsernameRow[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [cursor, setCursor] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [mintModalOpen, setMintModalOpen] = useState(false);

  async function load(next = false) {
    setBusy(true);
    setError("");
    const params = new URLSearchParams({ limit: "50" });
    if (status !== "all") params.set("status", status);
    if (query) params.set("q", query);
    if (next && cursor) params.set("before_id", cursor);
    try {
      const result = await api.collectibleUsernames(params);
      const page = result.rows ?? [];
      setRows((current) => (next ? [...current, ...page] : page));
      setCursor(result.next_before_id ?? "");
      setHasMore(Boolean(result.has_more));
      setLoaded(true);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void load(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [status, query]);

  const columns: Column<CollectibleUsernameRow>[] = [
    { key: "username", label: "Username", sortValue: (row) => row.Username, render: (row) => <strong>{displayUsername(row.Username)}</strong> },
    { key: "status", label: "Status", sortValue: (row) => row.Status, render: (row) => <UsernameStatus status={row.Status} /> },
    { key: "owner", label: "Owner", sortValue: (row) => ownerLabel(row, ""), render: (row) => ownerLabel(row, "—") },
    { key: "price", label: "Price", className: "mono", align: "right", firstDir: "desc", sortValue: (row) => Number(row.Amount) || 0, render: (row) => priceLabel(row) },
    { key: "transfers", label: "Transfers", align: "right", firstDir: "desc", sortValue: (row) => Number(row.TransferCount) || 0, render: (row) => row.TransferCount },
    {
      key: "updated",
      label: "Updated",
      firstDir: "desc",
      sortValue: (row) => new Date(row.UpdatedAt).getTime() || 0,
      render: (row) => <span title={formatDate(row.UpdatedAt)}>{formatRelative(row.UpdatedAt) || "—"}</span>
    }
  ];

  return (
    <PageFrame
      title={"Usernames"}
      actions={
        <>
          <button className="btn primary icon-text" type="button" onClick={() => setMintModalOpen(true)}>
            <Plus /> {"Mint username"}
          </button>
          <button className="icon-btn" type="button" aria-label={"Refresh"} title={"Refresh"} onClick={() => void load(false)} disabled={busy}>
            <ArrowsRotateRight className={busy ? "spin" : undefined} />
          </button>
        </>
      }
    >
      {error && <Alert>{error}</Alert>}
      <div className="list-controls">
        <label className="searchbox">
          <Magnifier />
          <input value={q} onChange={(event) => setQ(event.target.value)} placeholder={"Search by username"} aria-label={"Search usernames"} />
        </label>
        {STATUS_VIEWS.map((view) => (
          <FilterChip key={view.value} label={view.label} active={status === view.value} onToggle={() => setStatus(view.value)} />
        ))}
      </div>
      <DataTable
        columns={columns}
        rows={rows}
        rowKey={(row) => row.ID}
        onRowClick={(row) => navigate(`/collectible-usernames/${row.ID}`)}
        loading={busy && !loaded}
        empty={query || status !== "all" ? "No usernames match." : "No collectible usernames minted yet."}
      />
      <div className="table-footer">
        <span>{rows.length > 0 ? `${rows.length} shown` : ""}</span>
        {hasMore && (
          <button className="btn" type="button" onClick={() => void load(true)} disabled={busy}>
            {"Load more"}
          </button>
        )}
      </div>

      {mintModalOpen && (
        <MintCollectibleUsernameModal
          onClose={() => setMintModalOpen(false)}
          onMinted={() => void load(false)}
        />
      )}
    </PageFrame>
  );
}

export function UsernameStatus({ status }: { status: CollectibleUsernameStatus }) {
  if (status === "owned") return <Badge tone="good">{"Owned"}</Badge>;
  if (status === "burned") return <Badge tone="danger"><Flame size={12} /> {"Burned"}</Badge>;
  return <Badge><Vault size={12} /> {"Vault"}</Badge>;
}

export function ownerLabel(row: CollectibleUsernameRow, vaultLabel: string): string {
  if (!row.OwnerPeerType || row.OwnerPeerID === "" || row.OwnerPeerID === "0") return vaultLabel;
  const name = displayUsername(row.OwnerUsername) || row.OwnerName || row.OwnerPeerID;
  return `${name} · ${row.OwnerPeerType}:${row.OwnerPeerID}`;
}

// priceLabel renders what a Telegram client will draw, not the stored integer:
// both legs are smallest units on the wire (see formatCurrency).
export function priceLabel(row: CollectibleUsernameRow): string {
  const base = formatCurrency(row.Amount, row.Currency);
  if (row.CryptoCurrency && row.CryptoAmount && row.CryptoAmount !== "0") {
    return `${formatCurrency(row.CryptoAmount, row.CryptoCurrency)} (${base})`;
  }
  return base;
}
