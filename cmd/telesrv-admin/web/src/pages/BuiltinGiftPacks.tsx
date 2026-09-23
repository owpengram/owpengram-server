import { ArrowLeft, Boxes, Cake, ChevronLeft, ChevronRight, Crown, Eye, Gavel, Hammer, Hash, LifeBuoy, Loader2, Repeat, Search, Sparkles, UserRound, X, type LucideIcon } from "lucide-react";
import lottie from "lottie-web/build/player/lottie_light_canvas";
import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { Alert } from "../components/ui";
import type { BuiltinGift, BuiltinGiftPack } from "../types";

function PackAnimation({ packID, slug }: { packID: string; slug: string }) {
  const host = useRef<HTMLDivElement>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let cancelled = false;
    let animation: ReturnType<typeof lottie.loadAnimation> | null = null;
    api.builtinGiftPackAnimation(packID, slug).then((data) => {
      if (cancelled || !host.current) return;
      animation = lottie.loadAnimation({
        container: host.current,
        renderer: "canvas",
        loop: true,
        autoplay: true,
        animationData: structuredClone(data)
      });
    }).catch(() => setFailed(true));
    return () => {
      cancelled = true;
      animation?.destroy();
    };
  }, [packID, slug]);

  return <div className="gift-pack-anim" ref={host}>{failed && <span className="gift-pack-anim-error">{"Preview unavailable"}</span>}</div>;
}

function giftCount(pack: BuiltinGiftPack) {
  return `${pack.gifts.length} ${pack.gifts.length === 1 ? "gift" : "gifts"} · by ${pack.author}`;
}

function ImportPackButton({ pack, onDone }: { pack: BuiltinGiftPack; onDone: () => void }) {
  return (
    <ActionButton
      tone="primary"
      label={`Import ${pack.name}`}
      icon={<Boxes size={15} />}
      path="/api/actions/import-builtin-gift-pack"
      payload={() => ({ pack_id: pack.id })}
      onDone={onDone}
    />
  );
}

// flagIcon picks the glyph for one server-side mechanic label (see
// giftDef.flags in internal/seed/giftpacks/registry.go).
function flagIcon(flag: string): LucideIcon {
  if (flag.startsWith("auction")) return Gavel;
  if (flag.startsWith("limited")) return Hash;
  if (flag.startsWith("resale")) return Repeat;
  if (flag.endsWith("per user")) return UserRound;
  switch (flag) {
    case "premium only": return Crown;
    case "birthday": return Cake;
    case "support only": return LifeBuoy;
    case "craftable": return Hammer;
    default: return Sparkles;
  }
}

function FlagIcon({ flag, size = 12 }: { flag: string; size?: number }) {
  const Icon = flagIcon(flag);
  return <Icon size={size} aria-hidden="true" />;
}

// chance renders an attribute's odds among the random-draw pool; craft-only
// models are never drawn and show their rarity instead.
function chance(attr: { permille: number; rarity?: string }, pool: { permille: number; rarity?: string }[]) {
  if (attr.rarity) return `craft only · ${attr.rarity}`;
  const total = pool.filter((a) => !a.rarity).reduce((sum, a) => sum + a.permille, 0);
  if (total <= 0) return "";
  const pct = (attr.permille / total) * 100;
  return `${pct.toFixed(pct < 10 ? 1 : 0).replace(/\.0$/, "")}%`;
}

function GiftDetail({ pack, gift, siblings, onSelect, onBack }: {
  pack: BuiltinGiftPack;
  gift: BuiltinGift;
  siblings: BuiltinGift[];
  onSelect: (slug: string) => void;
  onBack: () => void;
}) {
  const upgrade = gift.upgrade;
  const flags = gift.flags ?? [];
  const firstBackdrop = upgrade?.backdrops[0];
  const modelBg = firstBackdrop ? `radial-gradient(circle at 50% 42%, ${firstBackdrop.center}, ${firstBackdrop.edge})` : undefined;
  const at = siblings.findIndex((s) => s.slug === gift.slug);
  const prev = at > 0 ? siblings[at - 1] : null;
  const next = at >= 0 && at < siblings.length - 1 ? siblings[at + 1] : null;
  return (
    <div className="gift-detail">
      <div className="gift-detail-nav">
        <button className="btn compact-btn" type="button" onClick={onBack}><ArrowLeft size={14} /> {"All gifts"}</button>
        <span className="gift-detail-position">{at >= 0 ? `${at + 1} / ${siblings.length}` : ""}</span>
        <button className="btn compact-btn" type="button" disabled={!prev} onClick={() => prev && onSelect(prev.slug)} aria-label={"Previous gift"}><ChevronLeft size={14} /> {"Prev"}</button>
        <button className="btn compact-btn" type="button" disabled={!next} onClick={() => next && onSelect(next.slug)} aria-label={"Next gift"}>{"Next"} <ChevronRight size={14} /></button>
      </div>
      <div className="gift-detail-head">
        <div className="gift-detail-hero"><PackAnimation packID={pack.id} slug={gift.slug} /></div>
        <div className="gift-detail-info">
          <h3>{gift.title}</h3>
          <div className="gift-detail-price">
            <strong>⭐ {gift.stars}</strong>
            <span>{`converts to ⭐ ${gift.convert_stars}`}</span>
          </div>
          {flags.length > 0 ? (
            <div className="gift-detail-props">
              {flags.map((flag) => <span className="gift-detail-prop" key={flag}><FlagIcon flag={flag} size={13} /> {flag}</span>)}
            </div>
          ) : <p className="gift-pack-desc">{"No modifiers: a plain gift anyone can buy."}</p>}
          {upgrade && <div className="gift-detail-upgrade">{`Upgrade for ⭐ ${upgrade.stars} · supply ${upgrade.supply.toLocaleString()}`}</div>}
        </div>
      </div>
      {upgrade ? <>
        <h4 className="gift-detail-section">{"Models"} <span>{upgrade.models.length}</span></h4>
        <div className="gift-variant-grid">
          {upgrade.models.map((model) => (
            <figure className={`gift-variant ${model.rarity ? "crafted" : ""}`} key={model.id}>
              <div className="gift-variant-art" style={{ background: modelBg }}><PackAnimation packID={pack.id} slug={model.id} /></div>
              <figcaption><strong>{model.name}</strong><span>{chance(model, upgrade.models)}</span></figcaption>
            </figure>
          ))}
        </div>
        <h4 className="gift-detail-section">{"Symbols"} <span>{upgrade.patterns.length}</span></h4>
        <div className="gift-variant-grid small">
          {upgrade.patterns.map((pattern) => (
            <figure className="gift-variant" key={pattern.id}>
              <div className="gift-variant-art symbol"><PackAnimation packID={pack.id} slug={pattern.id} /></div>
              <figcaption><strong>{pattern.name}</strong><span>{chance(pattern, upgrade.patterns)}</span></figcaption>
            </figure>
          ))}
        </div>
        <h4 className="gift-detail-section">{"Backdrops"} <span>{upgrade.backdrops.length}</span></h4>
        <div className="gift-variant-grid small">
          {upgrade.backdrops.map((backdrop) => (
            <figure className="gift-variant" key={backdrop.name}>
              <div
                className="gift-backdrop-swatch"
                style={{ background: `radial-gradient(circle, ${backdrop.pattern} 1.6px, transparent 2.6px) 0 0 / 16px 16px, radial-gradient(circle at 50% 42%, ${backdrop.center}, ${backdrop.edge})` }}
              >
                <strong style={{ color: backdrop.text }}>{backdrop.name}</strong>
              </div>
              <figcaption><span>{chance(backdrop, upgrade.backdrops)}</span></figcaption>
            </figure>
          ))}
        </div>
      </> : <p className="gift-pack-desc gift-detail-noupgrade">{"This gift can't be upgraded to a collectible."}</p>}
    </div>
  );
}

function PackPreviewModal({ pack, onClose, onImported }: { pack: BuiltinGiftPack; onClose: () => void; onImported: () => void }) {
  const [selected, setSelected] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const body = useRef<HTMLDivElement>(null);
  const selectedGift = pack.gifts.find((gift) => gift.slug === selected) ?? null;
  const needle = query.trim().toLowerCase();
  // A pack can run to a dozen gifts; the filter matches the title and the
  // mechanic labels, so "auction" or "upgradeable" narrows it down too.
  const shown = useMemo(() => !needle ? pack.gifts : pack.gifts.filter((gift) =>
    gift.title.toLowerCase().includes(needle) || (gift.flags ?? []).some((flag) => flag.toLowerCase().includes(needle))
  ), [pack.gifts, needle]);

  useEffect(() => { body.current?.scrollTo({ top: 0 }); }, [selected]);

  useEffect(() => {
    function onKey(event: KeyboardEvent) {
      // The import confirmation opens its own modal on top; let Escape
      // belong to that one instead of tearing down the flow underneath it.
      if (event.key !== "Escape" || document.querySelectorAll(".modal-backdrop").length !== 1) return;
      if (selected) setSelected(null);
      else onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, selected]);

  return createPortal(
    <div className="modal-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
      <section className="modal command-modal gift-pack-modal" role="dialog" aria-modal="true" aria-label={pack.name}>
        <div className="modal-head">
          <div className="gift-pack-modal-title">
            <PackAnimation packID={pack.id} slug={pack.icon} />
            <div>
              <div className="eyebrow">{"Built-in gift pack"}</div>
              <h2>{pack.name}</h2>
              <span className="gift-pack-meta">{giftCount(pack)}</span>
            </div>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} aria-label={"Close"}><X size={15} /></button>
        </div>
        <div className="gift-pack-modal-body" ref={body}>
          {selectedGift ? (
            <GiftDetail
              pack={pack}
              gift={selectedGift}
              // Prev/Next walks whatever the filter left on screen, so the
              // arrows match the grid the gift was opened from.
              siblings={shown.some((gift) => gift.slug === selectedGift.slug) ? shown : pack.gifts}
              onSelect={setSelected}
              onBack={() => setSelected(null)}
            />
          ) : <>
            <p className="gift-pack-desc">{pack.description}</p>
            <div className="gift-pack-modal-toolbar">
              <label className="searchbox"><Search size={15} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={"Filter by gift title or mechanic"} /></label>
              <span className="gift-pack-meta">{`Showing ${shown.length} of ${pack.gifts.length}`}</span>
            </div>
            <div className="gift-pack-preview-grid">
              {shown.map((gift) => (
                <button className="gift-pack-tile" type="button" key={gift.slug} onClick={() => setSelected(gift.slug)} aria-label={`${gift.title} details`}>
                  <PackAnimation packID={pack.id} slug={gift.slug} />
                  <span className="gift-pack-tile-title">{gift.title}</span>
                  <span className="gift-pack-tile-price">⭐ {gift.stars}</span>
                  {(gift.flags ?? []).length > 0 && (
                    <span className="gift-pack-mods">
                      {gift.flags.map((flag) => <span className="gift-pack-mod" key={flag} title={flag}><FlagIcon flag={flag} /></span>)}
                    </span>
                  )}
                </button>
              ))}
              {shown.length === 0 && <p className="gift-pack-desc">{"No gift matches that filter."}</p>}
            </div>
          </>}
        </div>
        <div className="modal-actions">
          <button className="btn" type="button" onClick={onClose}>{"Close"}</button>
          <ImportPackButton pack={pack} onDone={() => { onClose(); onImported(); }} />
        </div>
      </section>
    </div>,
    document.body
  );
}

// Built-in packs ship inside the server (internal/seed/giftpacks) and import
// through the same path as an uploaded pack; gifts already in the catalog by
// title are skipped, so importing again is safe.
export function BuiltinGiftPacks({ onImported }: { onImported: () => void }) {
  const [packs, setPacks] = useState<BuiltinGiftPack[] | null>(null);
  const [error, setError] = useState("");
  const [previewPack, setPreviewPack] = useState<BuiltinGiftPack | null>(null);

  useEffect(() => {
    api.builtinGiftPacks().then((res) => setPacks(res.packs ?? [])).catch((err) => setError(errorMessage(err)));
  }, []);

  return (
    <section className="section-block">
      <h2>{"Built-in packs"}</h2>
      <div className="card-body">
        {error && <Alert>{error}</Alert>}
        {packs === null && !error && <div className="gift-pack-loading"><Loader2 className="spin" size={16} /> {"Loading packs…"}</div>}
        {packs?.length === 0 && <p className="gift-pack-desc">{"No built-in packs."}</p>}
        {packs && packs.length > 0 && (
          <div className="gift-pack-grid">
            {packs.map((pack) => (
              <article className="gift-pack-card" key={pack.id}>
                <button className="gift-pack-cover" type="button" onClick={() => setPreviewPack(pack)} aria-label={`Preview ${pack.name}`}>
                  <PackAnimation packID={pack.id} slug={pack.icon} />
                </button>
                <div className="gift-pack-info">
                  <strong className="gift-pack-name">{pack.name}</strong>
                  <span className="gift-pack-meta">{giftCount(pack)}</span>
                  <p className="gift-pack-desc">{pack.description}</p>
                </div>
                <div className="gift-pack-actions">
                  <button className="btn" type="button" onClick={() => setPreviewPack(pack)}><Eye size={15} /> {"Preview"}</button>
                  <ImportPackButton pack={pack} onDone={onImported} />
                </div>
              </article>
            ))}
          </div>
        )}
      </div>
      {previewPack && <PackPreviewModal pack={previewPack} onClose={() => setPreviewPack(null)} onImported={onImported} />}
    </section>
  );
}
