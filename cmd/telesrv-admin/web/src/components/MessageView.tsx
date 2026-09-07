import {
  Contact,
  Dice5,
  FileText,
  Gift,
  Image,
  Link2,
  ListChecks,
  MapPin,
  Radio,
  Settings2,
  Sparkles,
  type LucideIcon
} from "lucide-react";
import type { ReactNode } from "react";
import { formatBytes } from "../lib/format";

// What a message actually was, rendered the way a person reads it: the text
// first, then what was attached to it. The database rows behind it are still
// available further down each detail page, but an operator opening a message
// is nearly always asking "what does it say", and answering that with a JSON
// dump made them decode the answer themselves.

// Mirrors domain.MessageMedia's JSON. Everything is optional because the
// snapshot is written by a server that keeps gaining media kinds -- an unknown
// one has to degrade to "there is media of kind X" rather than blow up.
type MediaSnapshot = {
  kind?: string;
  document?: { file_name?: string; mime_type?: string; size?: number; duration?: number };
  photo?: { id?: number | string };
  contact?: { first_name?: string; last_name?: string; phone_number?: string };
  geo?: { lat?: number; long?: number };
  geo_live?: { lat?: number; long?: number; period?: number };
  venue?: { title?: string; address?: string };
  poll?: { question?: string; answers?: unknown[]; closed?: boolean };
  web_page?: { url?: string; title?: string; site_name?: string };
  story?: { id?: number };
  todo?: { title?: string };
  dice?: { emoticon?: string; value?: number };
  giveaway?: unknown;
  service_action?: { kind?: string; type?: string };
  spoiler?: boolean;
  ttl_seconds?: number;
  voice?: boolean;
  round?: boolean;
  video?: boolean;
};

function parseMedia(raw: string | undefined): MediaSnapshot | null {
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as MediaSnapshot;
    if (!parsed || typeof parsed !== "object") return null;
    // "{}" is what a text-only message stores, not a media object.
    if (Object.keys(parsed).length === 0) return null;
    return parsed;
  } catch {
    return null;
  }
}

// describeMedia turns the snapshot into a line a person can read plus the icon
// that goes with it. Unknown kinds still get a row, named after the kind.
function describeMedia(media: MediaSnapshot): { icon: LucideIcon; title: string; detail: string } {
  const kind = media.kind ?? "";
  switch (kind) {
    case "photo":
      return { icon: Image, title: "Photo", detail: media.photo?.id ? `id ${media.photo.id}` : "" };
    case "document": {
      const doc = media.document ?? {};
      const bits = [doc.mime_type, doc.size ? formatBytes(String(doc.size)) : "", doc.duration ? `${doc.duration}s` : ""].filter(Boolean);
      const title = media.voice ? "Voice message" : media.round ? "Round video" : media.video ? "Video" : "File";
      return { icon: FileText, title, detail: [doc.file_name, bits.join(" · ")].filter(Boolean).join(" — ") };
    }
    case "contact": {
      const c = media.contact ?? {};
      const name = [c.first_name, c.last_name].filter(Boolean).join(" ");
      return { icon: Contact, title: "Contact", detail: [name, c.phone_number].filter(Boolean).join(" · ") };
    }
    case "geo":
      return { icon: MapPin, title: "Location", detail: media.geo ? `${media.geo.lat}, ${media.geo.long}` : "" };
    case "geo_live":
      return { icon: MapPin, title: "Live location", detail: media.geo_live ? `${media.geo_live.lat}, ${media.geo_live.long}` : "" };
    case "venue":
      return { icon: MapPin, title: "Venue", detail: [media.venue?.title, media.venue?.address].filter(Boolean).join(" — ") };
    case "poll":
      return {
        icon: ListChecks,
        title: media.poll?.closed ? "Poll (closed)" : "Poll",
        detail: [media.poll?.question, media.poll?.answers ? `${media.poll.answers.length} options` : ""].filter(Boolean).join(" — ")
      };
    case "web_page":
      return { icon: Link2, title: "Link preview", detail: [media.web_page?.title, media.web_page?.url].filter(Boolean).join(" — ") };
    case "story":
      return { icon: Sparkles, title: "Story", detail: media.story?.id ? `id ${media.story.id}` : "" };
    case "todo":
      return { icon: ListChecks, title: "Checklist", detail: media.todo?.title ?? "" };
    case "dice":
      return { icon: Dice5, title: "Dice", detail: [media.dice?.emoticon, media.dice?.value].filter(Boolean).join(" ") };
    case "giveaway":
      return { icon: Gift, title: "Giveaway", detail: "" };
    case "service":
      return { icon: Settings2, title: "Service action", detail: media.service_action?.kind ?? media.service_action?.type ?? "" };
    default:
      return { icon: Radio, title: kind ? `Media (${kind})` : "Media", detail: "" };
  }
}

export function MessageView({
  body,
  media,
  sender,
  meta,
  badges
}: {
  body: string;
  media?: string;
  // Who sent it, already resolved to something readable by the caller.
  sender: string;
  // When, and anything else that belongs on the header line.
  meta: string;
  badges?: ReactNode;
}) {
  const parsed = parseMedia(media);
  const described = parsed ? describeMedia(parsed) : null;
  const Icon = described?.icon;
  const text = body?.trim() ?? "";

  return (
    <section className="message-view">
      <div className="message-view-head">
        <div>
          <strong>{sender}</strong>
          <small>{meta}</small>
        </div>
        {badges && <div className="entity-badges">{badges}</div>}
      </div>

      <div className="message-bubble">
        {text
          ? <p className="message-text">{text}</p>
          : <p className="message-text empty">{described ? "No caption" : "No text"}</p>}

        {described && Icon && (
          <div className="message-attachment">
            <span className="message-attachment-icon"><Icon size={16} /></span>
            <span className="message-attachment-copy">
              <strong>{described.title}</strong>
              {described.detail && <small>{described.detail}</small>}
            </span>
          </div>
        )}

        {parsed && (parsed.spoiler || parsed.ttl_seconds) && (
          <div className="message-flags">
            {parsed.spoiler && <span className="chip">{"Spoiler"}</span>}
            {parsed.ttl_seconds ? <span className="chip">{`Self-destructs after ${parsed.ttl_seconds}s`}</span> : null}
          </div>
        )}
      </div>
    </section>
  );
}
