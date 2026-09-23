import { Boxes, Eye, Loader2, X } from "lucide-react";
import lottie from "lottie-web/build/player/lottie_light_canvas";
import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { Alert } from "../components/ui";
import type { BuiltinGiftPack } from "../types";

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

function PackPreviewModal({ pack, onClose, onImported }: { pack: BuiltinGiftPack; onClose: () => void; onImported: () => void }) {
  useEffect(() => {
    function onKey(event: KeyboardEvent) {
      // The import confirmation opens its own modal on top; let Escape
      // belong to that one instead of tearing down the flow underneath it.
      if (event.key === "Escape" && document.querySelectorAll(".modal-backdrop").length === 1) onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

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
        <div className="gift-pack-modal-body">
          <p className="gift-pack-desc">{pack.description}</p>
          <div className="gift-pack-preview-grid">
            {pack.gifts.map((gift) => (
              <figure className="gift-pack-tile" key={gift.slug}>
                <PackAnimation packID={pack.id} slug={gift.slug} />
                <figcaption>
                  <strong>{gift.title}</strong>
                  <span>⭐ {gift.stars}</span>
                </figcaption>
              </figure>
            ))}
          </div>
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
        <p className="gift-import-note"><span>{"Original gift sets that ship with OwpenGram. Preview a pack, then import it. Gifts already in the catalog (matched by title) are skipped, so importing again is safe."}</span></p>
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
