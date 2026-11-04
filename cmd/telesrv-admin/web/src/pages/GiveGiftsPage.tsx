import { ArrowsRotateRight, Gift, Magnifier } from "@gravity-ui/icons";
import { useEffect, useMemo, useState } from "react";
import { api, errorMessage } from "../api";
import { StaticLottie } from "../components/StaticLottie";
import { Alert, PageFrame } from "../components/ui";
import type { StarGiftCatalogRow } from "../types";
import { GiveGiftForm } from "./GiveGiftForm";

// Give Gifts: pick a catalog gift from the grid on the left, send it on the
// right. Delivery is free and comes from the system account (777000).
export function GiveGiftsPage() {
  const [gifts, setGifts] = useState<StarGiftCatalogRow[]>([]);
  const [query, setQuery] = useState("");
  const [showDisabled, setShowDisabled] = useState(false);
  const [selected, setSelected] = useState<StarGiftCatalogRow | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function load() {
    setBusy(true);
    setError("");
    try {
      const rows = (await api.starGiftCatalog()).rows ?? [];
      setGifts(rows);
      setSelected((current) => (current ? rows.find((g) => g.GiftID === current.GiftID) ?? current : rows.find((g) => g.Enabled) ?? rows[0] ?? null));
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void load();
  }, []);

  const disabledCount = gifts.filter((g) => !g.Enabled).length;
  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return gifts.filter(
      (gift) =>
        (showDisabled || gift.Enabled) &&
        (!needle || String(gift.GiftID).includes(needle) || gift.Title.toLowerCase().includes(needle))
    );
  }, [gifts, query, showDisabled]);

  return (
    <PageFrame
      title={"Give Gifts"}
      actions={
        <button className="icon-btn" type="button" aria-label={"Refresh"} title={"Refresh"} onClick={() => void load()} disabled={busy}>
          <ArrowsRotateRight className={busy ? "spin" : undefined} />
        </button>
      }
    >
      {error && <Alert>{error}</Alert>}
      <div className="give-layout">
        <section className="give-shelf">
          <div className="give-shelf-head">
            <label className="searchbox">
              <Magnifier />
              <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={"Search gifts"} aria-label={"Search gifts"} />
            </label>
            {disabledCount > 0 && (
              <label className="give-shelf-toggle">
                <input type="checkbox" checked={showDisabled} onChange={(event) => setShowDisabled(event.target.checked)} />
                {`Show disabled (${disabledCount})`}
              </label>
            )}
          </div>
          <div className="give-shelf-grid" role="listbox" aria-label={"Select a gift"}>
            {visible.map((gift) => {
              const active = selected?.GiftID === gift.GiftID;
              return (
                <button
                  key={gift.GiftID}
                  type="button"
                  role="option"
                  aria-selected={active}
                  className={`give-tile ${active ? "active" : ""} ${gift.Enabled ? "" : "disabled"}`}
                  onClick={() => setSelected(gift)}
                  title={gift.Title}
                >
                  <StaticLottie className="give-tile-art" cacheKey={`${gift.GiftID}:${gift.Revision}`} loader={() => api.giftAnimation(gift.GiftID)} />
                  <span className="give-tile-title">{gift.Title || `Gift #${gift.GiftID}`}</span>
                  <span className="give-tile-price">{gift.Enabled ? `★ ${Number(gift.Stars).toLocaleString()}` : "Disabled"}</span>
                </button>
              );
            })}
            {visible.length === 0 && !busy && <div className="give-shelf-empty">{"No gifts match."}</div>}
          </div>
          <div className="give-shelf-foot">{`${visible.length} of ${gifts.length} gifts`}</div>
        </section>
        <section className="give-panel">
          {selected ? (
            <GiveGiftForm key={selected.GiftID} gift={selected} onDone={() => void load()} />
          ) : (
            <div className="give-panel-empty">
              <Gift width={28} height={28} />
              <p>{"Pick a gift on the left to send it."}</p>
            </div>
          )}
        </section>
      </div>
    </PageFrame>
  );
}
