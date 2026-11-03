import { ArrowDownToLine, Check, Copy, Xmark } from "@gravity-ui/icons";
import QRCode from "qrcode";
import { useEffect, useMemo, useState } from "react";
import { createPortal } from "react-dom";
import { api, errorMessage } from "../api";
import { copyToClipboard } from "../clipboard";
import { Alert, LoadingSurface } from "./ui";

// AddServerLinkModal hands out this server: the https address of its public
// page (internal/web serverlanding.go), which carries the owpg://addserver
// link -- as a QR code and as text. https, because owpg:// is ignored by most
// apps and camera scanners; the page then offers "Add to OwpenGram". Without a
// configured public URL it falls back to the owpg:// link itself. The link carries host+port only (see the
// Go handler's doc comment for why name/description/key/DC are never embedded),
// and the client's "Add Server" form fetches the rest straight from the server.

// The QR is drawn as SVG from the module matrix rather than as a library image,
// so the centre can be left empty for the server's own logo. Level H keeps the
// code readable with that much of it covered.
const QR_SIZE = 248;
const LOGO_SHARE = 0.24;

function qrMatrix(text: string): { size: number; dark: (x: number, y: number) => boolean } {
  const code = QRCode.create(text, { errorCorrectionLevel: "H" });
  const size = code.modules.size;
  return { size, dark: (x, y) => Boolean(code.modules.get(y, x)) };
}

// The three corner finder patterns are drawn as rounded squares of their own;
// the generic dot loop skips them. Their corners stay within one module of
// rounding: past that, stricter decoders (OpenCV) stop finding them at all.
function inFinder(x: number, y: number, size: number): boolean {
  const near = (v: number, start: number) => v >= start && v < start + 7;
  return (near(x, 0) && near(y, 0)) || (near(x, size - 7) && near(y, 0)) || (near(x, 0) && near(y, size - 7));
}

function QrSvg({ link, logo }: { link: string; logo: string }) {
  const { size, dark } = useMemo(() => qrMatrix(link), [link]);
  // Four modules of quiet zone is what the spec asks for; scanners give up on
  // codes that sit too close to the card edge.
  const quiet = 4;
  const view = size + quiet * 2;
  // The modules hidden under the logo plate: a centred square, rounded to whole
  // modules so the plate never cuts a dot in half.
  const hole = Math.ceil(size * LOGO_SHARE) | 1;
  const holeStart = (size - hole) / 2;
  const covered = (x: number, y: number) =>
    x >= holeStart - 0.5 && x < holeStart + hole - 0.5 && y >= holeStart - 0.5 && y < holeStart + hole - 0.5;

  // Neighbouring modules melt into one shape: each module is a square whose
  // corner is rounded only where nothing continues it, so runs read as smooth
  // strokes and a lone module as a dot. All of it is one path.
  const on = (x: number, y: number) =>
    x >= 0 && y >= 0 && x < size && y < size && dark(x, y) && !inFinder(x, y, size) && !covered(x, y);
  const r = 0.5;
  let d = "";
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      if (!on(x, y)) continue;
      const up = on(x, y - 1);
      const down = on(x, y + 1);
      const left = on(x - 1, y);
      const right = on(x + 1, y);
      const tl = !up && !left ? r : 0;
      const tr = !up && !right ? r : 0;
      const br = !down && !right ? r : 0;
      const bl = !down && !left ? r : 0;
      const X = x + quiet;
      const Y = y + quiet;
      d +=
        `M${X + tl} ${Y}H${X + 1 - tr}` +
        (tr ? `A${tr} ${tr} 0 0 1 ${X + 1} ${Y + tr}` : "") +
        `V${Y + 1 - br}` +
        (br ? `A${br} ${br} 0 0 1 ${X + 1 - br} ${Y + 1}` : "") +
        `H${X + bl}` +
        (bl ? `A${bl} ${bl} 0 0 1 ${X} ${Y + 1 - bl}` : "") +
        `V${Y + tl}` +
        (tl ? `A${tl} ${tl} 0 0 1 ${X + tl} ${Y}` : "") +
        "Z";
    }
  }
  const finders = [
    [0, 0],
    [size - 7, 0],
    [0, size - 7]
  ].map(([fx, fy]) => (
    <g key={`${fx}-${fy}`} transform={`translate(${fx + quiet} ${fy + quiet})`}>
      <rect width={7} height={7} rx={1} />
      <rect x={1} y={1} width={5} height={5} rx={0.2} fill="#ffffff" />
      <rect x={2} y={2} width={3} height={3} rx={0.9} />
    </g>
  ));
  const plate = hole * 0.92;
  const plateAt = quiet + (size - plate) / 2;

  return (
    <svg className="share-qr" viewBox={`0 0 ${view} ${view}`} width={QR_SIZE} height={QR_SIZE} role="img" aria-label={"QR code for the add-server link"}>
      <rect width={view} height={view} fill="#ffffff" />
      {/* The hairline stroke closes the anti-aliasing seams between touching modules. */}
      <path d={d} fill="currentColor" stroke="currentColor" strokeWidth={0.04} />
      <g fill="currentColor">{finders}</g>
      <rect x={plateAt} y={plateAt} width={plate} height={plate} rx={plate * 0.28} fill="#ffffff" />
      <image href={logo} x={plateAt + plate * 0.1} y={plateAt + plate * 0.1} width={plate * 0.8} height={plate * 0.8} preserveAspectRatio="xMidYMid meet" style={{ clipPath: "inset(0 round 22%)" }} />
    </svg>
  );
}

// downloadPng renders the on-screen SVG (logo included) to a PNG file, for
// printing or a website button. The logo is same-origin, so the canvas stays
// exportable.
async function downloadPng(svg: SVGSVGElement, fileName: string) {
  const clone = svg.cloneNode(true) as SVGSVGElement;
  const logo = clone.querySelector("image");
  const href = logo?.getAttribute("href");
  if (logo && href) {
    const blob = await fetch(href).then((r) => r.blob());
    const dataURL = await new Promise<string>((resolve) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result));
      reader.readAsDataURL(blob);
    });
    logo.setAttribute("href", dataURL);
  }
  clone.setAttribute("width", "1024");
  clone.setAttribute("height", "1024");
  clone.style.color = "#0b0b12";
  const source = new XMLSerializer().serializeToString(clone);
  const image = new Image();
  image.src = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(source)}`;
  await image.decode();
  const canvas = document.createElement("canvas");
  canvas.width = 1024;
  canvas.height = 1024;
  canvas.getContext("2d")?.drawImage(image, 0, 0, 1024, 1024);
  const a = document.createElement("a");
  a.href = canvas.toDataURL("image/png");
  a.download = fileName;
  a.click();
}

export function AddServerLinkModal({ onClose }: { onClose: () => void }) {
  const [link, setLink] = useState<string | null>(null);
  // The owpg:// link, kept only for the "host:port" line under the name.
  const [appLink, setAppLink] = useState("");
  const [identity, setIdentity] = useState<{ name: string; hasIcon: boolean } | null>(null);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);
  const [svgEl, setSvgEl] = useState<HTMLDivElement | null>(null);

  useEffect(() => {
    let cancelled = false;
    api.addServerLink()
      .then((result) => {
        if (cancelled) return;
        setAppLink(result.link);
        setLink(result.share_url || result.link);
      })
      .catch((err) => {
        if (!cancelled) setError(errorMessage(err));
      });
    api.serverIdentity()
      .then((info) => {
        if (!cancelled) setIdentity({ name: info.name, hasIcon: Boolean(info.icon_ext) });
      })
      .catch(() => {
        if (!cancelled) setIdentity({ name: "", hasIcon: false });
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    function onKey(event: KeyboardEvent) {
      if (event.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const logo = useMemo(() => (identity?.hasIcon ? api.serverIconURL() : "/logo.png"), [identity?.hasIcon]);
  const name = identity?.name.trim() || "OwpenGram";
  const address = useMemo(() => {
    if (!appLink) return "";
    try {
      const params = new URL(appLink).searchParams;
      return `${params.get("host") ?? ""}${params.get("port") ? `:${params.get("port")}` : ""}`;
    } catch {
      return "";
    }
  }, [appLink]);

  async function copy() {
    if (!link) return;
    try {
      await copyToClipboard(link);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1800);
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  function download() {
    const svg = svgEl?.querySelector("svg");
    if (!svg) return;
    downloadPng(svg, `${name.replace(/[^\w.-]+/g, "-") || "server"}-qr.png`).catch((err) => setError(errorMessage(err)));
  }

  return createPortal(
    <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}>
      <section className="modal share-modal" role="dialog" aria-modal="true" aria-label={"Share server"}>
        <button className="icon-btn share-close" type="button" onClick={onClose} aria-label={"Close"}><Xmark /></button>
        <h2>{"Share server"}</h2>

        {error && <Alert>{error}</Alert>}
        {!link && !error && <LoadingSurface label={"Building link..."} />}
        {link && identity && (
          <>
            <div className="share-qr-card" ref={setSvgEl}>
              <QrSvg link={link} logo={logo} />
              <div className="share-identity">
                <strong>{name}</strong>
                {address && <span className="mono">{address}</span>}
              </div>
            </div>

            <div className="share-link">
              {/* A link to open, not a field to edit or select: copying has its own button. */}
              <a className="mono share-link-url" href={link} target="_blank" rel="noopener noreferrer" title={link}>{link}</a>
            </div>

            <div className="share-actions">
              <button className="btn icon-text" type="button" onClick={download}>
                <ArrowDownToLine /> {"Download QR"}
              </button>
              <button className="btn primary icon-text" type="button" onClick={() => void copy()}>
                {copied ? <Check /> : <Copy />} {copied ? "Copied" : "Copy link"}
              </button>
            </div>

            {/* <p className="share-note">
              {"Opens this server's page, with a button that adds it to the app. Only the address travels in the link; the app gets the name and key from the server itself."}
            </p> */}
          </>
        )}
      </section>
    </div>,
    document.body
  );
}
