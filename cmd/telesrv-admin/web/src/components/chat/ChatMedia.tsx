import { ArrowDownToLine, File, ListCheck, Link, MapPin, Person, Picture } from "@gravity-ui/icons";
import lottie from "lottie-web/build/player/lottie_light_canvas";
import { useEffect, useRef, useState } from "react";
import { formatBytes } from "../../lib/format";
import { privateView, type ChatMedia } from "../../lib/privateView";

// How one attachment draws inside a bubble or a grid tile. Every file comes
// through /api/private/media with the page's view grant.

export function formatDuration(seconds?: number): string {
  if (!seconds || seconds <= 0) return "";
  const s = Math.round(seconds);
  const m = Math.floor(s / 60);
  return `${m}:${String(s % 60).padStart(2, "0")}`;
}

export function mediaLabel(kind?: string): string {
  switch (kind) {
    case "photo":
      return "Photo";
    case "video":
      return "Video";
    case "round":
      return "Video message";
    case "gif":
      return "GIF";
    case "sticker":
      return "Sticker";
    case "voice":
      return "Voice message";
    case "audio":
      return "Audio";
    case "file":
      return "File";
    case "contact":
      return "Contact";
    case "geo":
      return "Location";
    case "poll":
      return "Poll";
    case "webpage":
      return "Link";
    case "dice":
      return "Dice";
    case "story":
      return "Story";
    case "service":
      return "Service message";
    default:
      return kind ? "Attachment" : "";
  }
}

// urlFor turns a storage key into a URL; by default the private viewer's
// media route with the page's grant (reports pass their own).
export type MediaURLFor = (key: string, lottie?: boolean) => string;

export function Lightbox({ media, grant, urlFor, onClose }: { media: ChatMedia; grant: string; urlFor?: MediaURLFor; onClose: () => void }) {
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => event.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  const isVideo = media.kind === "video" || media.kind === "round" || media.kind === "gif";
  const src = media.full ? (urlFor ? urlFor(media.full) : privateView.mediaURL(media.full, grant)) : "";
  return (
    <div className="lightbox" role="dialog" aria-modal="true" onClick={onClose}>
      {isVideo ? (
        <video src={src} controls autoPlay onClick={(event) => event.stopPropagation()} />
      ) : (
        <img src={src} alt="" />
      )}
    </div>
  );
}

function LottieFromURL({ url, size }: { url: string; size: number }) {
  const host = useRef<HTMLDivElement>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let cancelled = false;
    let anim: ReturnType<typeof lottie.loadAnimation> | null = null;
    fetch(url, { credentials: "same-origin" })
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(r.statusText))))
      .then((data) => {
        if (cancelled || !host.current) return;
        anim = lottie.loadAnimation({ container: host.current, renderer: "canvas", loop: true, autoplay: true, animationData: data });
      })
      .catch(() => !cancelled && setFailed(true));
    return () => {
      cancelled = true;
      anim?.destroy();
    };
  }, [url]);
  if (failed) return <div className="media-sticker-fallback" style={{ width: size, height: size }} />;
  return <div ref={host} style={{ width: size, height: size }} />;
}

// fitBox scales a w×h attachment into the bubble's maximum box.
function fitBox(w = 0, h = 0, max = 320): { width: number; height: number } {
  if (!w || !h) return { width: max, height: Math.round(max * 0.66) };
  const scale = Math.min(1, max / Math.max(w, h));
  return { width: Math.max(120, Math.round(w * scale)), height: Math.max(80, Math.round(h * scale)) };
}

export function MediaView({ media, grant, urlFor, onOpen }: { media: ChatMedia; grant: string; urlFor?: MediaURLFor; onOpen?: (media: ChatMedia) => void }) {
  const [revealed, setRevealed] = useState(!media.spoiler);
  const [broken, setBrokenState] = useState(false);
  // Why the file failed, asked of the server once it did -- a bare "can't be
  // previewed" hides whether it was a permission, a missing file or the size.
  const [why, setWhy] = useState("");
  function setBroken(value: boolean) {
    setBrokenState(value);
    const probe = url(media.thumb || media.full);
    if (!value || !probe) return;
    fetch(probe, { credentials: "same-origin", cache: "no-store" })
      .then(async (r) => {
        if (r.ok) {
          setWhy(`the browser could not decode ${r.headers.get("content-type") || "the file"}`);
          return;
        }
        const body = await r.json().catch(() => null);
        setWhy(`HTTP ${r.status}${body?.error ? `: ${body.error}` : ""}`);
      })
      .catch((err) => setWhy(String(err)));
  }
  const url = (key?: string, lottieJSON = false) => (key ? (urlFor ? urlFor(key, lottieJSON) : privateView.mediaURL(key, grant, lottieJSON)) : "");

  const fileCard = (note?: string) => (
    <a className="media-file" href={url(media.full)} download={media.file_name || undefined}>
      <span className="media-file-icon">{media.kind === "voice" || media.kind === "audio" ? <ArrowDownToLine /> : <File />}</span>
      <span className="media-file-text">
        <strong>{media.file_name || media.title || mediaLabel(media.kind)}</strong>
        <span>{[media.size ? formatBytes(String(media.size)) : "", media.mime_type, note].filter(Boolean).join(" · ")}</span>
      </span>
    </a>
  );

  if (broken && media.full) return fileCard(why ? `can't be previewed (${why})` : "can't be previewed");

  const spoiler = (node: JSX.Element) =>
    revealed ? (
      node
    ) : (
      <button type="button" className="media-spoiler" onClick={() => setRevealed(true)}>
        {node}
        <span>{"Spoiler — click to show"}</span>
      </button>
    );

  switch (media.kind) {
    case "photo": {
      const box = fitBox(media.w, media.h);
      if (!media.thumb) return <div className="media-missing"><Picture /> {"Photo"}</div>;
      return spoiler(
        <img
          className="media-photo"
          src={url(media.thumb)}
          alt=""
          loading="lazy"
          style={box}
          onClick={() => revealed && onOpen?.(media)}
          onError={() => setBroken(true)}
        />
      );
    }
    case "video":
    case "gif": {
      const box = fitBox(media.w, media.h);
      const gif = media.kind === "gif";
      return spoiler(
        <div className="media-video" style={box}>
          <video
            src={url(media.full)}
            poster={media.thumb ? url(media.thumb) : undefined}
            controls={!gif}
            autoPlay={gif}
            loop={gif}
            muted={gif}
            playsInline
            preload={gif ? "auto" : "none"}
            onError={() => setBroken(true)}
          />
          {!gif && media.duration ? <span className="media-badge">{formatDuration(media.duration)}</span> : null}
          {gif && <span className="media-badge">{"GIF"}</span>}
        </div>
      );
    }
    case "round":
      return (
        <video
          className="media-round"
          src={url(media.full)}
          poster={media.thumb ? url(media.thumb) : undefined}
          controls
          playsInline
          preload="none"
          onError={() => setBroken(true)}
        />
      );
    case "sticker": {
      if (media.animated && media.full) return <LottieFromURL url={url(media.full, true)} size={160} />;
      if (media.mime_type?.startsWith("video/")) {
        return <video className="media-sticker" src={url(media.full)} autoPlay loop muted playsInline />;
      }
      return <img className="media-sticker" src={url(media.full || media.thumb)} alt={media.title || ""} loading="lazy" onError={() => setBroken(true)} />;
    }
    case "voice":
    case "audio":
      return (
        <div className="media-audio">
          {media.kind === "audio" && (media.title || media.subtitle) && (
            <div className="media-audio-title">
              <strong>{media.title || media.file_name}</strong>
              {media.subtitle && <span>{media.subtitle}</span>}
            </div>
          )}
          <audio src={url(media.full)} controls preload="none" onError={() => setBroken(true)} />
        </div>
      );
    case "file":
      return fileCard();
    case "contact":
      return (
        <div className="media-card">
          <Person />
          <span>
            <strong>{media.title || "Contact"}</strong>
            {media.subtitle && <span>{media.subtitle}</span>}
          </span>
        </div>
      );
    case "geo":
      return (
        <div className="media-card">
          <MapPin />
          <span>
            <strong>{"Location"}</strong>
          </span>
        </div>
      );
    case "poll":
      return (
        <div className="media-card">
          <ListCheck />
          <span>
            <strong>{media.title || "Poll"}</strong>
            <span>{"Poll"}</span>
          </span>
        </div>
      );
    case "webpage":
      return (
        <div className="media-card media-webpage">
          <Link />
          <span>
            <strong>{media.title || "Link preview"}</strong>
            {media.subtitle && <span>{media.subtitle}</span>}
          </span>
        </div>
      );
    default:
      return <div className="media-missing">{mediaLabel(media.kind)}</div>;
  }
}

// MediaTile is a square grid cell (media tab, profile photos, stories).
export function MediaTile({ media, grant, onOpen, children }: { media?: ChatMedia; grant: string; onOpen?: () => void; children?: React.ReactNode }) {
  const src = media?.thumb ? privateView.mediaURL(media.thumb, grant) : "";
  const isVideo = media?.kind === "video" || media?.kind === "round" || media?.kind === "gif";
  return (
    <button type="button" className="media-tile" onClick={onOpen}>
      {src ? <img src={src} alt="" loading="lazy" /> : <span className="media-tile-empty"><Picture /></span>}
      {isVideo && <span className="media-badge">{formatDuration(media?.duration) || "Video"}</span>}
      {children}
    </button>
  );
}
