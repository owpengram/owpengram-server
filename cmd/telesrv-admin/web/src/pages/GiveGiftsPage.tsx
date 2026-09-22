import { Gift, RefreshCw, Search } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { api, errorMessage } from "../api";
import { StaticLottie } from "../components/StaticLottie";
import { Alert, Badge, PageFrame } from "../components/ui";
import type { StarGiftCatalogRow } from "../types";
import { GiveGiftForm } from "./GiveGiftForm";

export function GiveGiftsPage() {
  const [gifts, setGifts] = useState<StarGiftCatalogRow[]>([]);
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<StarGiftCatalogRow | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function load() {
    setBusy(true);
    setError("");
    try {
      const rows = (await api.starGiftCatalog()).rows ?? [];
      setGifts(rows);
      setSelected((current) => current ?? rows[0] ?? null);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => { void load(); }, []);

  const visible = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    if (!normalized) return gifts;
    return gifts.filter((gift) =>
      String(gift.GiftID).includes(normalized) || gift.Title.toLowerCase().includes(normalized)
    );
  }, [gifts, query]);

  return (
    <PageFrame title={"Give Gifts"} eyebrow={"Grant catalog gifts to any user or channel"} actions={
      <button className="btn" type="button" onClick={() => load()} disabled={busy}><RefreshCw size={15} /> {"Refresh"}</button>
    }>
      {error && <Alert>{error}</Alert>}
      <p className="give-gift-upgrade-note">{"Pick a gift to grant. Delivery is free of charge and sent from the system account 777000 (OwpenGram) by default."}</p>
      <div className="give-gift-layout">
        <section className="give-gift-picker">
          <div className="give-gift-picker-head">
            <label className="searchbox"><Search size={15} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={"Search by title or gift ID"} /></label>
            <span className="gift-list-summary">{`Showing ${visible.length} of ${gifts.length}`}</span>
          </div>
          <div className="give-gift-picker-list" role="listbox" aria-label={"Select a gift"}>
            {visible.map((gift) => {
              const active = selected?.GiftID === gift.GiftID;
              return (
                <button key={gift.GiftID} type="button" role="option" aria-selected={active}
                  className={`give-gift-option ${active ? "selected" : ""} ${gift.Enabled ? "" : "gift-row-disabled"}`}
                  onClick={() => setSelected(gift)}>
                  <StaticLottie className="give-gift-thumb" cacheKey={`${gift.GiftID}:${gift.Revision}`} loader={() => api.giftAnimation(gift.GiftID)} />
                  <span className="give-gift-option-info">
                    <strong>{gift.Title || `Gift #${gift.GiftID}`}</strong>
                    <span className="mono">#{gift.GiftID}</span>
                  </span>
                  <span className="give-gift-option-price">
                    {gift.Enabled ? <Badge>⭐ {gift.Stars}</Badge> : <Badge tone="neutral">{"Disabled"}</Badge>}
                  </span>
                </button>
              );
            })}
            {visible.length === 0 && !busy && <div className="official-gift-empty">{"No results"}</div>}
          </div>
        </section>
        <section className="give-gift-panel">
          {selected
            ? <GiveGiftForm key={selected.GiftID} gift={selected} onDone={() => void load()} />
            : <div className="give-gift-empty-panel"><Gift size={26} /><p>{"Select a gift from the list to start."}</p></div>}
        </section>
      </div>
    </PageFrame>
  );
}
