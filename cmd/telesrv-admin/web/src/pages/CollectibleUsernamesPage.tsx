import { AtSign, ChevronDown, ChevronRight, Flame, Loader2, Plus, RefreshCw, Search, Vault } from "lucide-react";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import { MintCollectibleUsernameModal } from "../components/MintCollectibleUsernameModal";
import { Alert, Badge, EmptyRow, Metric, PageFrame, QueryPanel } from "../components/ui";
import { displayUsername, formatCurrency, formatDate } from "../lib/format";
import type { Navigate } from "../routing";
import type { CollectibleUsernameRow, CollectibleUsernameStatus } from "../types";

type StatusFilter = "all" | CollectibleUsernameStatus;

export function CollectibleUsernamesPage({ navigate }: { navigate: Navigate }) {
  const [status, setStatus] = useState<StatusFilter>("all");
  const [q, setQ] = useState("");
  const [limit, setLimit] = useState("50");
  const [rows, setRows] = useState<CollectibleUsernameRow[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [cursor, setCursor] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [mintModalOpen, setMintModalOpen] = useState(false);

  async function load(next = false) {
    setBusy(true);
    setError("");
    const params = new URLSearchParams({ limit });
    if (status !== "all") params.set("status", status);
    if (q.trim()) params.set("q", q.trim().replace(/^@/, ""));
    if (next && cursor) params.set("before_id", cursor);
    try {
      const result = await api.collectibleUsernames(params);
      const page = result.rows ?? [];
      setRows((current) => (next ? [...current, ...page] : page));
      setCursor(result.next_before_id ?? "");
      setHasMore(Boolean(result.has_more));
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void load(false);
  }, []);

  const vaultCount = rows.filter((row) => row.Status === "vault").length;
  const ownedCount = rows.filter((row) => row.Status === "owned").length;
  const burnedCount = rows.filter((row) => row.Status === "burned").length;

  return (
    <PageFrame
      title={"Collectible usernames"}
      eyebrow={"NFT usernames / Registry"}
      actions={
        <>
          <button className="btn primary icon-text" type="button" onClick={() => setMintModalOpen(true)}>
            <Plus size={15} /> {"Mint username"}
          </button>
          <button className="btn icon-text" type="button" onClick={() => load(false)} disabled={busy}>
            <RefreshCw size={15} className={busy ? "spin" : ""} /> {"Refresh"}
          </button>
        </>
      }
    >
      {error && <Alert>{error}</Alert>}
      <div className="metric-row">
        <Metric label={"Loaded rows"} value={String(rows.length)} />
        <Metric label={"In vault"} value={String(vaultCount)} />
        <Metric label={"Held by owners"} value={String(ownedCount)} tone="good" />
        <Metric label={"Burned"} value={String(burnedCount)} tone={burnedCount ? "danger" : "neutral"} />
      </div>

      <QueryPanel>
        <form className="toolbar" onSubmit={(event) => { event.preventDefault(); void load(false); }}>
          <label className="searchbox">
            <Search size={15} />
            <input value={q} onChange={(event) => setQ(event.target.value)} placeholder={"Search by username"} />
          </label>
          <label className="field-inline">
            <span>{"Status"}</span>
            <select value={status} onChange={(event) => setStatus(event.target.value as StatusFilter)}>
              <option value="all">{"All statuses"}</option>
              <option value="vault">{"Vault"}</option>
              <option value="owned">{"Owned"}</option>
              <option value="burned">{"Burned"}</option>
            </select>
          </label>
          <label className="field-inline">
            <span>{"Limit"}</span>
            <input className="small-input" value={limit} onChange={(event) => setLimit(event.target.value)} type="number" min="1" max="200" />
          </label>
          <button className="btn primary icon-text" type="submit" disabled={busy}>
            {busy ? <Loader2 size={15} className="spin" /> : <Search size={15} />} {"Search"}
          </button>
        </form>
      </QueryPanel>

      <div className="table-wrap">
        <table className="data-table">
          <thead>
            <tr>
              <th>{"Username"}</th>
              <th>{"Status"}</th>
              <th>{"Owner"}</th>
              <th>{"Price"}</th>
              <th>{"Purchase date (UTC)"}</th>
              <th>{"Transfers"}</th>
              <th>{"Updated"}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.ID}>
                <td><strong>{displayUsername(row.Username)}</strong></td>
                <td><UsernameStatus status={row.Status} /></td>
                <td>{ownerLabel(row, "Vault")}</td>
                <td className="mono">{priceLabel(row)}</td>
                <td>{formatDate(row.PurchaseDate) || "-"}</td>
                <td className="mono">{row.TransferCount}</td>
                <td>{formatDate(row.UpdatedAt) || "-"}</td>
                <td>
                  <button className="row-link" type="button" onClick={() => navigate(`/collectible-usernames/${row.ID}`)}>
                    <AtSign size={14} /> {"Details"} <ChevronRight size={14} />
                  </button>
                </td>
              </tr>
            ))}
            {rows.length === 0 && <EmptyRow colSpan={8} />}
          </tbody>
        </table>
      </div>
      {hasMore && (
        <div className="toolbar">
          <button className="btn icon-text" type="button" onClick={() => load(true)} disabled={busy}>
            {busy ? <Loader2 size={15} className="spin" /> : <ChevronDown size={15} />} {"Load more"}
          </button>
        </div>
      )}

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
