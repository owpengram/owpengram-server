import {
  AtSign,
  BadgeCheck,
  Bot,
  ChevronDown,
  Database,
  Film,
  LayoutDashboard,
  LogOut,
  Megaphone,
  MessageSquareText,
  Settings,
  Share2,
  ShieldAlert,
  ShieldCheck,
  Smile,
  Stamp,
  Users,
  Zap,
	Sticker
} from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { api, errorMessage } from "../api";
import { permissionBotVerificationReview, permissionServerManage, permissionVerificationReview, useCan, useThirdPartyVerificationHidden } from "../permissions";
import { type Navigate, type RouteState, routeTitle } from "../routing";
import { ThemeSwitch } from "../theme";
import { AddServerLinkModal } from "./AddServerLinkModal";
import { AppLink } from "./AppLink";

// Compresses a sorted (or unsorted) list of layer numbers into run-length
// ranges for the compact sidebar label, e.g. [225,226,227,228,229] -> "225-229",
// or [225,226,228] -> "225-226, 228" if the server ever supports a
// non-contiguous set. The full list is still always shown in the tooltip.
function formatLayerRanges(layers: number[]): string {
  const sorted = [...layers].sort((a, b) => a - b);
  const parts: string[] = [];
  let start = sorted[0];
  let prev = sorted[0];
  for (let i = 1; i <= sorted.length; i++) {
    const current = sorted[i];
    if (current === prev + 1) {
      prev = current;
      continue;
    }
    parts.push(start === prev ? `${start}` : `${start}-${prev}`);
    start = current;
    prev = current;
  }
  return parts.join(", ");
}

export function BootScreen() {
  return (
    <div className="boot-screen">
      <div className="brand compact brand-elevated">
        <span className="brand-mark"><img src="/logo.png" alt="OwpenGram" /></span>
        <span>
          <strong>OwpenGram</strong>
          <small>{"Admin Console"}</small>
        </span>
      </div>
      <div className="loader-bar" />
    </div>
  );
}

export function Shell({
  actor,
  apiLayers,
  build,
  route,
  navigate,
  onLogout,
  children
}: {
  actor: string;
  apiLayers?: number[];
  build?: { commit: string; short_commit: string; dirty: boolean; build_time: string };
  route: RouteState;
  navigate: Navigate;
  onLogout: () => void;
  children: ReactNode;
}) {
  // The verification queue is hidden for a session without verification.review:
  // the entry would only lead to a 403 (and the route itself is gated as well).
  const canReviewVerification = useCan(permissionVerificationReview);
  // Same reasoning for the third-party queue, which has its own right: the two
  // sections are granted independently, so one entry can be visible without the other.
  const canReviewBotVerification = useCan(permissionBotVerificationReview);
  const canManageServer = useCan(permissionServerManage);
  const [addServerLinkOpen, setAddServerLinkOpen] = useState(false);
  const [connecting, setConnecting] = useState(false);
  const [connectError, setConnectError] = useState("");

  // "Connect" is the short-cut version of Share: instead of showing a link to
  // copy elsewhere, it opens the owpg://addserver link (host+port only --
  // see the Go handler's doc comment for why nothing else ever goes in it)
  // right here in this browser, so if an OwpenGram client is registered for
  // that scheme on this machine, it launches straight into "Add Server"
  // pre-filled for the server this very admin panel manages, fetching the
  // rest (name/description/key) straight from it.
  async function connectThisServer() {
    setConnecting(true);
    setConnectError("");
    try {
      const result = await api.addServerLink();
      window.location.href = result.link;
    } catch (err) {
      setConnectError(errorMessage(err));
    } finally {
      setConnecting(false);
    }
  }
  // Server identity (name/icon) is admin-editable per Server Settings ->
  // Server identity, and takes over the sidebar branding when set -- the
  // operator's own server should look like their server, not like the
  // "OwpenGram" reference build, once they've bothered to configure it.
  // Only fetched for sessions that can even see Server Settings; a session
  // without that permission just gets the default branding.
  const [identity, setIdentity] = useState<{ name: string; iconExt?: string } | null>(null);
  useEffect(() => {
    if (!canManageServer) return;
    api.serverIdentity()
      .then((info) => setIdentity({ name: info.name, iconExt: info.icon_ext }))
      .catch(() => undefined);
  }, [canManageServer]);
  const [brandIconFailed, setBrandIconFailed] = useState(false);
  const brandName = identity?.name?.trim() || "OwpenGram";
  const brandIconSrc = identity?.iconExt && !brandIconFailed ? api.serverIconURL() : "/logo.png";

  // The browser tab (title + favicon) follows the same custom-identity
  // override as the sidebar brand above, so a re-labeled server actually
  // looks like itself in the tab strip too, not just inside the app.
  useEffect(() => {
    document.title = `${brandName} Admin`;
  }, [brandName]);
  useEffect(() => {
    let link = document.querySelector<HTMLLinkElement>("link[rel='icon']");
    if (!link) {
      link = document.createElement("link");
      link.rel = "icon";
      document.head.appendChild(link);
    }
    const linkEl = link;
    const src = identity?.iconExt && !brandIconFailed ? api.serverIconURL() : "/logo.png";
    // Browsers render the favicon file as-is -- they don't apply the
    // sidebar's CSS border-radius to it, so a square-cornered source image
    // (the default logo, or whatever shape an operator's uploaded icon
    // happens to be) shows up square in the tab strip. Bake the circular
    // mask into the actual pixels instead, the same way an app icon export
    // would, so the tab matches the round mark everywhere else in the UI.
    let cancelled = false;
    const img = new Image();
    img.crossOrigin = "anonymous";
    img.onload = () => {
      if (cancelled) {
        return;
      }
      const size = 64;
      const canvas = document.createElement("canvas");
      canvas.width = size;
      canvas.height = size;
      const ctx = canvas.getContext("2d");
      if (!ctx) {
        linkEl.href = src;
        return;
      }
      ctx.save();
      ctx.beginPath();
      ctx.arc(size / 2, size / 2, size / 2, 0, Math.PI * 2);
      ctx.closePath();
      ctx.clip();
      ctx.drawImage(img, 0, 0, size, size);
      ctx.restore();
      linkEl.href = canvas.toDataURL("image/png");
    };
    img.onerror = () => {
      // Cross-origin or load failure: fall back to the raw image rather
      // than leaving the tab with no favicon at all.
      if (!cancelled) {
        linkEl.href = src;
      }
    };
    img.src = src;
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [identity?.iconExt, brandIconFailed]);
  // Third-party verification is additionally hidden by default (not fully
  // finished) regardless of what the session was granted -- see permissions.tsx.
  const thirdPartyVerificationHidden = useThirdPartyVerificationHidden();
  const messagesActive = route.path.startsWith("/messages");
  const [messagesOpen, setMessagesOpen] = useState(messagesActive);

  useEffect(() => {
    if (messagesActive) {
      setMessagesOpen(true);
    }
  }, [messagesActive]);

  async function logout() {
    await api.logout().catch(() => undefined);
    onLogout();
  }

  return (
    <div className="shell">
      <aside className="sidebar">
        <AppLink className="brand" href="/" navigate={navigate}>
          <span className="brand-mark"><img src={brandIconSrc} alt={brandName} onError={() => setBrandIconFailed(true)} /></span>
          <span>
            <strong>{brandName}</strong>
            <small>{"Admin Console"}</small>
          </span>
        </AppLink>
        <div className="sidebar-label">{"Navigation"}</div>
        <nav className="nav-list" aria-label={"Primary navigation"}>
          <NavLink icon={<LayoutDashboard size={16} />} href="/" route={route} navigate={navigate}>{"Overview"}</NavLink>
          <NavLink icon={<Users size={16} />} href="/accounts" route={route} navigate={navigate}>{"Accounts"}</NavLink>
          <NavLink icon={<ShieldCheck size={16} />} href="/channels" route={route} navigate={navigate}>{"Supergroups / Channels"}</NavLink>
          <NavLink icon={<Bot size={16} />} href="/bots" route={route} navigate={navigate}>{"Bots"}</NavLink>
          <NavLink icon={<ShieldAlert size={16} />} href="/moderation" route={route} navigate={navigate}>{"Reports / Moderation"}</NavLink>
          <NavLink icon={<Megaphone size={16} />} href="/broadcasts" route={route} navigate={navigate}>{"Broadcasts"}</NavLink>
          {canReviewVerification && (
            <NavLink icon={<BadgeCheck size={16} />} href="/verification" route={route} navigate={navigate}>{"Verification"}</NavLink>
          )}
          {canReviewBotVerification && !thirdPartyVerificationHidden && (
            <NavLink icon={<Stamp size={16} />} href="/bot-verification" route={route} navigate={navigate}>{"Third-party marks"}</NavLink>
          )}
          <NavLink icon={<AtSign size={16} />} href="/collectible-usernames" route={route} navigate={navigate}>{"NFT Usernames"}</NavLink>
          <NavLink icon={<Database size={16} />} href="/storage" route={route} navigate={navigate}>{"Storage"}</NavLink>
			<NavLink icon={<Sticker size={16} />} href="/stickers" route={route} navigate={navigate}>{"Stickers"}</NavLink>
			<NavLink icon={<Smile size={16} />} href="/emoji" route={route} navigate={navigate}>{"Emoji"}</NavLink>
			<NavLink icon={<Film size={16} />} href="/gif-catalog" route={route} navigate={navigate}>{"GIFs"}</NavLink>
          <div className={`nav-section ${messagesActive ? "active" : ""} ${messagesOpen ? "open" : ""}`}>
            <button
              className="nav-section-toggle"
              type="button"
              aria-expanded={messagesOpen}
              onClick={() => setMessagesOpen((open) => !open)}
            >
              <MessageSquareText size={16} />
              <span>{"Messages"}</span>
              <ChevronDown className="nav-section-chevron" size={15} />
            </button>
            {messagesOpen && (
              <div className="nav-children">
                <NavLink
                  href="/messages/private"
                  route={route}
                  navigate={navigate}
                  activeWhen={(path) => path === "/messages" || path === "/messages/detail" || path.startsWith("/messages/private")}
                >
                  {"Private"}
                </NavLink>
                <NavLink
                  href="/messages/groups"
                  route={route}
                  navigate={navigate}
                  activeWhen={(path) => path.startsWith("/messages/groups")}
                >
                  {"Groups"}
                </NavLink>
              </div>
            )}
          </div>
          {canManageServer && (
            <NavLink icon={<Settings size={16} />} href="/server-settings" route={route} navigate={navigate}>{"Server Settings"}</NavLink>
          )}
        </nav>
        <div className="sidebar-status">
          <span className="sidebar-label">{"Version: O7"}</span>
          {apiLayers && apiLayers.length > 0 && (
            <span className="sidebar-label sidebar-api-layer" title={`Layers: ${apiLayers.join(", ")}`}>
              {`API layers: ${formatLayerRanges(apiLayers)}`}
            </span>
          )}
          {build?.short_commit && (
            <span className="sidebar-label sidebar-build" title={build.commit + (build.dirty ? " (uncommitted changes)" : "")}>
              {`Build: ${build.short_commit}${build.dirty ? "+" : ""}`}
            </span>
          )}
        </div>
        {canManageServer && (
          <div className="sidebar-server-actions">
            <button
              className="btn ghost sidebar-server-action"
              type="button"
              title={"Connect this browser's client to this server"}
              disabled={connecting}
              onClick={() => void connectThisServer()}
            >
              <Zap size={15} /> {"Connect"}
            </button>
            <button
              className="btn ghost sidebar-server-action"
              type="button"
              title={"Share server (get an add-server link)"}
              onClick={() => setAddServerLinkOpen(true)}
            >
              <Share2 size={15} /> {"Share"}
            </button>
          </div>
        )}
        {connectError && <div className="sidebar-server-action-error">{connectError}</div>}
        {addServerLinkOpen && <AddServerLinkModal onClose={() => setAddServerLinkOpen(false)} />}
      </aside>
      <div className="workspace">
        <header className="topbar">
          <div>
            <h1>{routeTitle(route.path)}</h1>
          </div>
          <div className="topbar-actions">
            <ThemeSwitch />
            <span className="actor-pill">{`Actor: ${actor}`}</span>
            <button className="btn ghost icon-text" type="button" onClick={logout} title={"Log out"}>
              <LogOut size={16} /> {"Log out"}
            </button>
          </div>
        </header>
        <main className="content">{children}</main>
      </div>
    </div>
  );
}

function NavLink({
  href,
  route,
  navigate,
  icon,
  children,
  activeWhen
}: {
  href: string;
  route: RouteState;
  navigate: Navigate;
  icon?: ReactNode;
  children: ReactNode;
  activeWhen?: (path: string) => boolean;
}) {
  const active = activeWhen ? activeWhen(route.path) : href === "/" ? route.path === "/" : route.path.startsWith(href);
  return (
    <AppLink className={`nav-item ${active ? "active" : ""}`} href={href} navigate={navigate}>
      {icon ?? <span aria-hidden="true" className="nav-dot" />}
      <span>{children}</span>
    </AppLink>
  );
}
