import lottie from "lottie-web/build/player/lottie_light_canvas";
import { useEffect, useRef, useState } from "react";
import { api, errorMessage } from "../api";

// One preview cell in a sticker/emoji set's preview grid. Mounted only while
// its modal is open, and only for the one set being viewed — not on the list
// page — so this never repeats the "100 animations on one page" lag.
//
// Documents come back either as Lottie JSON (TGS-animated) or as a raw raster
// image (static webp/png/etc, e.g. the "GestosLol" pack) — branch on the
// response's real Content-Type rather than assuming every document animates.
export function StickerDocumentPreview({ documentID, className = "", showError = true }: { documentID: string; className?: string; showError?: boolean }) {
  const host = useRef<HTMLDivElement>(null);
  const animation = useRef<ReturnType<typeof lottie.loadAnimation> | null>(null);
  const [error, setError] = useState("");
  const [imageURL, setImageURL] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    let objectURL: string | null = null;
    setError("");
    setImageURL(null);

    fetch(api.stickerDocumentAnimationURL(documentID), { credentials: "same-origin" })
      .then(async (response) => {
        if (!response.ok) {
          const body = await response.json().catch(() => null);
          throw new Error(body?.error || response.statusText);
        }
        const contentType = response.headers.get("content-type") ?? "";
        if (contentType.includes("json")) {
          const data = await response.json();
          if (cancelled || !host.current) return;
          animation.current?.destroy();
          animation.current = lottie.loadAnimation({
            container: host.current,
            renderer: "canvas",
            loop: true,
            autoplay: true,
            animationData: data
          });
          return;
        }
        const blob = await response.blob();
        if (cancelled) return;
        objectURL = URL.createObjectURL(blob);
        setImageURL(objectURL);
      })
      .catch((err) => {
        if (!cancelled) setError(errorMessage(err));
      });

    return () => {
      cancelled = true;
      animation.current?.destroy();
      animation.current = null;
      if (objectURL) URL.revokeObjectURL(objectURL);
    };
  }, [documentID]);

  return (
    <div className={`sticker-doc-cell ${className}`.trim()}>
      {imageURL ? (
        <img className="sticker-doc-image" src={imageURL} alt="" />
      ) : (
        <div className="sticker-doc-canvas" ref={host} />
      )}
      {error && showError && <span className="sticker-doc-error">{error}</span>}
    </div>
  );
}
