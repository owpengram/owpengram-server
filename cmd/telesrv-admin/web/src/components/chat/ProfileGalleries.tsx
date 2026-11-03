import { Gift, Pin } from "@gravity-ui/icons";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../../api";
import { privateView, type ChatMedia, type MediaItem, type ProfileContent, type ViewKind } from "../../lib/privateView";
import type { Navigate } from "../../routing";
import { useViewGrant } from "../PrivateGate";
import { StaticLottie } from "../StaticLottie";
import { Lightbox, MediaTile } from "./ChatMedia";

// The grids behind a profile's Photos / Posts / Gifts / Media tabs.

const shortDate = (unix: number) => (unix ? new Date(unix * 1000).toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" }) : "");

export function useProfileContent(kind: ViewKind, id: number) {
  const { grant, onError } = useViewGrant();
  const [content, setContent] = useState<ProfileContent | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let cancelled = false;
    privateView
      .profile(kind, id, grant)
      .then((result) => !cancelled && setContent(result))
      .catch((err) => !cancelled && !onError(err) && setError(errorMessage(err)));
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kind, id, grant]);
  return { content, error };
}

function Empty({ text }: { text: string }) {
  return <div className="gallery-empty">{text}</div>;
}

export function PhotosGallery({ content }: { content: ProfileContent }) {
  const { grant } = useViewGrant();
  const [open, setOpen] = useState<ChatMedia | null>(null);
  if (content.photos.length === 0) return <Empty text={"No profile photos."} />;
  return (
    <>
      <div className="gallery-grid">
        {content.photos.map((p) => {
          const media: ChatMedia = p.video
            ? { kind: "video", thumb: p.thumb, full: p.video }
            : { kind: "photo", thumb: p.thumb, full: p.full };
          return (
            <MediaTile key={p.photo_id} media={{ ...media, kind: "photo" }} grant={grant} onOpen={() => setOpen(media)}>
              <span className="tile-caption">
                {p.active && <span className="tile-tag good">{"current"}</span>}
                {p.kind === "fallback" && <span className="tile-tag">{"public"}</span>}
                {shortDate(p.date)}
              </span>
            </MediaTile>
          );
        })}
      </div>
      {open && <Lightbox media={open} grant={grant} onClose={() => setOpen(null)} />}
    </>
  );
}

export function StoriesGallery({ content }: { content: ProfileContent }) {
  const { grant } = useViewGrant();
  const [open, setOpen] = useState<ChatMedia | null>(null);
  if (content.stories.length === 0) return <Empty text={"No posts."} />;
  const now = Date.now() / 1000;
  return (
    <>
      <div className="gallery-grid stories">
        {content.stories.map((s) => (
          <MediaTile key={s.id} media={s.media} grant={grant} onOpen={() => s.media && setOpen(s.media)}>
            <span className="tile-caption">
              {s.pinned && <Pin />}
              {s.deleted ? (
                <span className="tile-tag danger">{"deleted"}</span>
              ) : s.expire_date > now ? (
                <span className="tile-tag good">{"live"}</span>
              ) : null}
              {shortDate(s.date)}
            </span>
            {s.caption && <span className="tile-text">{s.caption}</span>}
          </MediaTile>
        ))}
      </div>
      {open && <Lightbox media={open} grant={grant} onClose={() => setOpen(null)} />}
    </>
  );
}

export function GiftsGallery({ content, navigate }: { content: ProfileContent; navigate: Navigate }) {
  const [failed, setFailed] = useState<Record<number, boolean>>({});
  if (content.gifts.length === 0) return <Empty text={"No gifts."} />;
  return (
    <div className="gift-grid">
      {content.gifts.map((g) => (
        <div key={g.id} className={`gift-tile ${g.unique ? "unique" : ""}`}>
          <div className="gift-tile-art">
            {failed[g.gift_id] ? (
              <Gift width={40} height={40} />
            ) : (
              <StaticLottie
                cacheKey={`gift-${g.gift_id}`}
                loader={() => api.giftAnimation(String(g.gift_id))}
                onError={() => setFailed((prev) => ({ ...prev, [g.gift_id]: true }))}
              />
            )}
          </div>
          <strong>{g.unique_name || g.title || `Gift #${g.gift_id}`}</strong>
          <span className="gift-tile-stars">{`★ ${g.stars.toLocaleString()}`}</span>
          <span className="gift-tile-from">
            {g.from_user_id ? (
              <button type="button" className="link-btn" onClick={() => navigate(`/accounts/${g.from_user_id}`)}>
                {g.from_name || `User ${g.from_user_id}`}
              </button>
            ) : (
              "Anonymous"
            )}
            {g.name_hidden && " (hidden)"}
          </span>
          <span className="gift-tile-date">{shortDate(g.date)}</span>
          {g.message && <span className="gift-tile-message">{g.message}</span>}
          <span className="gift-tile-tags">
            {g.hidden && <span className="tile-tag">{"not on profile"}</span>}
            {g.converted && <span className="tile-tag">{"converted"}</span>}
          </span>
        </div>
      ))}
    </div>
  );
}

// AccountMediaGallery: every photo and video across the account's chats.
export function AccountMediaGallery({ userID }: { userID: number }) {
  const { grant, onError } = useViewGrant();
  const [items, setItems] = useState<MediaItem[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [busy, setBusy] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState("");
  const [open, setOpen] = useState<ChatMedia | null>(null);

  async function load(more: boolean) {
    setBusy(true);
    try {
      const last = items[items.length - 1];
      const result = await privateView.userMedia(userID, more && last ? { beforeDate: last.date, beforeID: last.id } : null, grant);
      setItems((prev) => (more ? [...prev, ...result.items] : result.items));
      setHasMore(result.has_more);
      setLoaded(true);
    } catch (err) {
      if (!onError(err)) setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void load(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userID, grant]);

  if (error) return <Empty text={error} />;
  if (!loaded) return <Empty text={"Loading…"} />;
  if (items.length === 0) return <Empty text={"No photos or videos in this account's chats."} />;
  return (
    <>
      <div className="gallery-grid">
        {items.map((item) => (
          <MediaTile key={`${item.peer_id}:${item.id}`} media={item.media} grant={grant} onOpen={() => setOpen(item.media)}>
            <span className="tile-caption">{shortDate(item.date)}</span>
          </MediaTile>
        ))}
      </div>
      {hasMore && (
        <div className="gallery-more">
          <button className="btn" type="button" disabled={busy} onClick={() => void load(true)}>
            {busy ? "Loading…" : "Load more"}
          </button>
        </div>
      )}
      {open && <Lightbox media={open} grant={grant} onClose={() => setOpen(null)} />}
    </>
  );
}
