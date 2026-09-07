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
  PanelLeftClose,
  PanelLeftOpen,
  Settings,
  UserCog,
  UserRound,
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
import { clearAdminCache } from "../lib/cache";
import { permissionBotVerificationReview, permissionServerManage, permissionAdminsManage,
  permissionAccountsRead,
  permissionChannelsRead,
  permissionBotsRead,
  permissionMessagesRead,
  permissionModerationReview,
  permissionBroadcastsRead,
  permissionStorageRead,
  permissionContentRead,
  permissionUsernamesRead,
  permissionVerificationReview, useCan, useThirdPartyVerificationHidden } from "../permissions";
import { type Navigate, type RouteState, routeTitle } from "../routing";
import { ThemeSwitch } from "../theme";
import { AddServerLinkModal } from "./AddServerLinkModal";
import { AppBackground } from "./AppBackground";
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
  const canManageAdmins = useCan(permissionAdminsManage);
  // Each section entry is hidden without the right to open it: the route is
  // gated server-side either way, so showing it would only lead to a 403.
  const canReadAccounts = useCan(permissionAccountsRead);
  const canReadChannels = useCan(permissionChannelsRead);
  const canReadBots = useCan(permissionBotsRead);
  const canReadMessages = useCan(permissionMessagesRead);
  const canReviewModeration = useCan(permissionModerationReview);
  const canReadBroadcasts = useCan(permissionBroadcastsRead);
  const canReadStorage = useCan(permissionStorageRead);
  const canReadContent = useCan(permissionContentRead);
  const canReadUsernames = useCan(permissionUsernamesRead);
  // Same reasoning for the third-party queue, which has its own right: the two
  // sections are granted independently, so one entry can be visible without the other.
  const canReviewBotVerification = useCan(permissionBotVerificationReview);
  const canManageServer = useCan(permissionServerManage);
  // Remembered per browser: an operator who works in a narrow window should not
  // have to re-collapse the navigation on every visit. A failed read (private
  // mode, blocked storage) just means the default.
  const [navCollapsed, setNavCollapsed] = useState(() => {
    try {
      return localStorage.getItem("owpengram.nav.collapsed") === "1";
    } catch {
      return false;
    }
  });

  function toggleNav() {
    setNavCollapsed((current) => {
      const next = !current;
      try {
        localStorage.setItem("owpengram.nav.collapsed", next ? "1" : "0");
      } catch {
        // Not being able to remember the choice is not a reason to refuse it.
      }
      return next;
    });
  }

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
  async function logout() {
    await api.logout().catch(() => undefined);
    // Drop the cached figures with the session. Without this the next operator
    // to sign in on this tab would open the dashboard on the previous one's
    // numbers -- briefly, but from an account that may not be allowed to see
    // them at all.
    clearAdminCache();
    onLogout();
  }

  return (
    <div className={`shell ${navCollapsed ? "shell--nav-collapsed" : ""}`.trim()}>
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
          {canReadAccounts && (
            <NavLink icon={<Users size={16} />} href="/accounts" route={route} navigate={navigate}>{"Accounts"}</NavLink>
          )}
          {canReadChannels && (
            <NavLink icon={<ShieldCheck size={16} />} href="/channels" route={route} navigate={navigate}>{"Supergroups / Channels"}</NavLink>
          )}
          {canReadBots && (
            <NavLink icon={<Bot size={16} />} href="/bots" route={route} navigate={navigate}>{"Bots"}</NavLink>
          )}
          {canReviewModeration && (
            <NavLink icon={<ShieldAlert size={16} />} href="/moderation" route={route} navigate={navigate}>{"Reports / Moderation"}</NavLink>
          )}
          {canReadBroadcasts && (
            <NavLink icon={<Megaphone size={16} />} href="/broadcasts" route={route} navigate={navigate}>{"Broadcasts"}</NavLink>
          )}
          {canReviewVerification && (
            <NavLink icon={<BadgeCheck size={16} />} href="/verification" route={route} navigate={navigate}>{"Verification"}</NavLink>
          )}
          {canReviewBotVerification && !thirdPartyVerificationHidden && (
            <NavLink icon={<Stamp size={16} />} href="/bot-verification" route={route} navigate={navigate}>{"Third-party marks"}</NavLink>
          )}
          {canReadUsernames && (
            <NavLink icon={<AtSign size={16} />} href="/collectible-usernames" route={route} navigate={navigate}>{"NFT Usernames"}</NavLink>
          )}
          {canReadStorage && (
            <NavLink icon={<Database size={16} />} href="/storage" route={route} navigate={navigate}>{"Storage"}</NavLink>
          )}
			{canReadContent && (
			  <NavLink icon={<Sticker size={16} />} href="/stickers" route={route} navigate={navigate}>{"Stickers"}</NavLink>
			)}
			{canReadContent && (
			  <NavLink icon={<Smile size={16} />} href="/emoji" route={route} navigate={navigate}>{"Emoji"}</NavLink>
			)}
			{canReadContent && (
			  <NavLink icon={<Film size={16} />} href="/gif-catalog" route={route} navigate={navigate}>{"GIFs"}</NavLink>
			)}
          {canReadMessages && (
            <NavLink
              icon={<MessageSquareText size={16} />}
              href="/messages/private"
              route={route}
              navigate={navigate}
              activeWhen={(path) => path.startsWith("/messages")}
            >
              {"Messages"}
            </NavLink>
          )}
          {canManageAdmins && (
            <NavLink icon={<UserCog size={16} />} href="/admin-users" route={route} navigate={navigate}>{"Operators"}</NavLink>
          )}
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
          <div className="topbar-lead">
            <button
              className="icon-btn nav-toggle"
              type="button"
              onClick={toggleNav}
              aria-expanded={!navCollapsed}
              aria-label={navCollapsed ? "Expand navigation" : "Collapse navigation"}
              title={navCollapsed ? "Expand navigation" : "Collapse navigation"}
            >
              {navCollapsed ? <PanelLeftOpen size={16} /> : <PanelLeftClose size={16} />}
            </button>
            <h1>{routeTitle(route.path)}</h1>
          </div>
          <div className="topbar-actions">
            <ThemeSwitch />
            <span className="actor-pill"><UserRound size={14} /> {actor}</span>
            <button className="btn ghost icon-text" type="button" onClick={logout} title={"Log out"}>
              <LogOut size={16} /> {"Log out"}
            </button>
          </div>
        </header>
        <main className="content">{children}</main>
      </div>
      {/* Behind everything, fixed to the viewport. The sidebar and topbar paint
          over it, so it shows through the working area only. */}
      <AppBackground className="app-background--workspace" />
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
    <AppLink
      className={`nav-item ${active ? "active" : ""}`}
      href={href}
      navigate={navigate}
      // The label is hidden when the sidebar is collapsed, so it moves to the
      // tooltip -- an icon rail with no names is a memory test.
      title={typeof children === "string" ? children : undefined}
    >
      {icon ?? <span aria-hidden="true" className="nav-dot" />}
      <span className="nav-item-label">{children}</span>
    </AppLink>
  );
}
