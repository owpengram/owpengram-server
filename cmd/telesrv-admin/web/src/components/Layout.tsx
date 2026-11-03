import {
  ArrowRightFromSquare,
  At,
  ChevronDown,
  ChevronsLeft,
  ChevronsRight,
  Comments,
  Database,
  Envelope,
  FaceRobot,
  FaceSmile,
  Gem,
  Filmstrip,
  Gear,
  Gift,
  Heart,
  House,
  Link,
  ListTimeline,
  Medal,
  Megaphone,
  Passport,
  Person,
  PersonGear,
  Picture,
  Persons,
  PlugConnection,
  SealCheck,
  Server,
  Shield,
  ShieldExclamation,
  Sparkles,
  Sticker,
  Wallet
} from "@gravity-ui/icons";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { api, errorMessage } from "../api";
import { clearAdminCache } from "../lib/cache";
import { permissionBotVerificationReview, permissionServerManage, permissionAdminsManage,
  permissionAuditRead,
  permissionPremiumManage,
  permissionDonationsManage,
  permissionRatingsRead,
  permissionStarGiftsRead,
  permissionStarGiftsManage,
  permissionAccountsRead,
  permissionChannelsRead,
  permissionBotsRead,
  permissionMessagesRead,
  permissionModerationReview,
  permissionBroadcastsRead,
  permissionStorageRead,
  permissionContentRead,
  permissionUsernamesRead,
  permissionVerificationReview, useCan, useDonationsEnabled, useThirdPartyVerificationHidden } from "../permissions";
import { type Navigate, type RouteState, routeSection, routeTitle } from "../routing";
import { AddServerLinkModal } from "./AddServerLinkModal";
import { AppBackground } from "./AppBackground";
import { AppLink } from "./AppLink";
import { PageHeaderContext } from "./pageHeader";

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
  version,
  update,
  route,
  navigate,
  onLogout,
  children
}: {
  actor: string;
  apiLayers?: number[];
  build?: { commit: string; short_commit: string; dirty: boolean; build_time: string };
  version?: string;
  update?: { available: boolean; latest?: string; url?: string; notes?: string };
  route: RouteState;
  navigate: Navigate;
  onLogout: () => void;
  children: ReactNode;
}) {
  // The verification queue is hidden for a session without verification.review:
  // the entry would only lead to a 403 (and the route itself is gated as well).
  const canReviewVerification = useCan(permissionVerificationReview);
  const canManageAdmins = useCan(permissionAdminsManage);
  const canReadAuditLog = useCan(permissionAuditRead);
  const canManagePremium = useCan(permissionPremiumManage);
  const canManageDonations = useCan(permissionDonationsManage);
  const canReadRatings = useCan(permissionRatingsRead);
  const canReadStarGifts = useCan(permissionStarGiftsRead);
  const canManageStarGifts = useCan(permissionStarGiftsManage);
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

  // The page heading lives in the top bar; pages portal their title and
  // actions into these two elements (see pageHeader.tsx).
  const [titleSlot, setTitleSlot] = useState<HTMLElement | null>(null);
  const [actionsSlot, setActionsSlot] = useState<HTMLElement | null>(null);
  const slots = useMemo(() => ({ title: titleSlot, actions: actionsSlot }), [titleSlot, actionsSlot]);
  const section = routeSection(route.path);

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
  // Donations are a server-wide feature switch (TELESRV_DONATIONS_ENABLED),
  // not a right -- see permissions.tsx. Off by default only if the operator
  // turned it off; the config default is on.
  const donationsEnabled = useDonationsEnabled();
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
        <nav className="nav-list" aria-label={"Primary navigation"}>
          <NavLink icon={<House />} href="/" route={route} navigate={navigate}>{"Overview"}</NavLink>

          {(canReadAccounts || canReadChannels || canReadMessages || canReadBots || canReadBroadcasts) && (
            <NavSection title={"Users and chats"} icon={<Persons />} route={route} collapsed={navCollapsed}
              paths={["/accounts", "/channels", "/messages", "/bots", "/broadcasts"]}>
              {canReadAccounts && (
                <NavLink icon={<Person />} href="/accounts" route={route} navigate={navigate}>{"Accounts"}</NavLink>
              )}
              {canReadChannels && (
                <NavLink icon={<Comments />} href="/channels" route={route} navigate={navigate}>{"Groups and channels"}</NavLink>
              )}
              {canReadMessages && (
                <NavLink
                  icon={<Envelope />}
                  href="/messages/private"
                  route={route}
                  navigate={navigate}
                  activeWhen={(path) => path.startsWith("/messages")}
                >
                  {"Messages"}
                </NavLink>
              )}
              {canReadBots && (
                <NavLink icon={<FaceRobot />} href="/bots" route={route} navigate={navigate}>{"Bots"}</NavLink>
              )}
              {canReadBroadcasts && (
                <NavLink icon={<Megaphone />} href="/broadcasts" route={route} navigate={navigate}>{"Broadcasts"}</NavLink>
              )}
            </NavSection>
          )}

          {(canReviewModeration || canReviewVerification || (canReviewBotVerification && !thirdPartyVerificationHidden)) && (
            <NavSection title={"Moderation"} icon={<Shield />} route={route} collapsed={navCollapsed}
              paths={["/moderation", "/verification", "/bot-verification"]}>
              {canReviewModeration && (
                <NavLink icon={<ShieldExclamation />} href="/moderation" route={route} navigate={navigate}>{"Reports"}</NavLink>
              )}
              {canReviewVerification && (
                <NavLink icon={<SealCheck />} href="/verification" route={route} navigate={navigate}>{"Verification"}</NavLink>
              )}
              {canReviewBotVerification && !thirdPartyVerificationHidden && (
                <NavLink icon={<Passport />} href="/bot-verification" route={route} navigate={navigate}>{"Third-party marks"}</NavLink>
              )}
            </NavSection>
          )}

          {canReadContent && (
            <NavSection title={"Content"} icon={<Picture />} route={route} collapsed={navCollapsed}
              paths={["/stickers", "/emoji", "/gif-catalog"]}>
              <NavLink icon={<Sticker />} href="/stickers" route={route} navigate={navigate}>{"Stickers"}</NavLink>
              <NavLink icon={<FaceSmile />} href="/emoji" route={route} navigate={navigate}>{"Emoji"}</NavLink>
              <NavLink icon={<Filmstrip />} href="/gif-catalog" route={route} navigate={navigate}>{"GIFs"}</NavLink>
            </NavSection>
          )}

          {(canManagePremium || (canManageDonations && donationsEnabled) || canReadRatings || canReadStarGifts || canManageStarGifts || canReadUsernames) && (
            <NavSection title={"Economy"} icon={<Gem />} route={route} collapsed={navCollapsed}
              paths={["/premium", "/donations", "/account-ratings", "/star-gift-catalog", "/give-gifts", "/collectible-usernames"]}>
              {canManagePremium && (
                <NavLink icon={<Sparkles />} href="/premium" route={route} navigate={navigate}>{"Premium"}</NavLink>
              )}
              {canManageDonations && donationsEnabled && (
                <NavLink icon={<Wallet />} href="/donations" route={route} navigate={navigate}>{"Donations"}</NavLink>
              )}
              {canReadRatings && (
                <NavLink icon={<Medal />} href="/account-ratings" route={route} navigate={navigate}>{"Ratings"}</NavLink>
              )}
              {canReadStarGifts && (
                <NavLink icon={<Gift />} href="/star-gift-catalog" route={route} navigate={navigate}>{"Gift catalog"}</NavLink>
              )}
              {canManageStarGifts && (
                <NavLink icon={<Heart />} href="/give-gifts" route={route} navigate={navigate}>{"Give gifts"}</NavLink>
              )}
              {canReadUsernames && (
                <NavLink icon={<At />} href="/collectible-usernames" route={route} navigate={navigate}>{"Usernames"}</NavLink>
              )}
            </NavSection>
          )}

          {(canReadStorage || canManageAdmins || canReadAuditLog || canManageServer) && (
            <NavSection title={"Server"} icon={<Server />} route={route} collapsed={navCollapsed}
              paths={["/storage", "/admin-users", "/audit-log", "/server-settings"]}>
              {canReadStorage && (
                <NavLink icon={<Database />} href="/storage" route={route} navigate={navigate}>{"Storage"}</NavLink>
              )}
              {canManageAdmins && (
                <NavLink icon={<PersonGear />} href="/admin-users" route={route} navigate={navigate}>{"Operators"}</NavLink>
              )}
              {canReadAuditLog && (
                <NavLink icon={<ListTimeline />} href="/audit-log" route={route} navigate={navigate}>{"Audit log"}</NavLink>
              )}
              {canManageServer && (
                <NavLink icon={<Gear />} href="/server-settings" route={route} navigate={navigate}>{"Settings"}</NavLink>
              )}
            </NavSection>
          )}
        </nav>
        <div className="sidebar-status">
          <span className="sidebar-label">{`Version: ${version ?? ""}`}</span>
          {update?.available && update.url && (
            <a
              className="sidebar-label sidebar-update"
              href={update.url}
              target="_blank"
              rel="noopener noreferrer"
              title={update.notes || "Open the release page"}
            >
              {`Update available: ${update.latest ?? ""}`}
            </a>
          )}
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
              <PlugConnection /> {"Connect"}
            </button>
            <button
              className="btn ghost sidebar-server-action"
              type="button"
              title={"Share server (get an add-server link)"}
              onClick={() => setAddServerLinkOpen(true)}
            >
              <Link /> {"Share"}
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
              {navCollapsed ? <ChevronsRight /> : <ChevronsLeft />}
            </button>
            {section && <span className="topbar-section">{section}</span>}
            <h1 className="topbar-title" ref={setTitleSlot}>
              <span className="topbar-default-title">{routeTitle(route.path)}</span>
            </h1>
          </div>
          <div className="topbar-actions">
            <div className="topbar-page-actions" ref={setActionsSlot} />
            <span className="actor-pill" title={actor}><Person /> {actor}</span>
            <button className="icon-btn" type="button" onClick={logout} aria-label={"Log out"} title={"Log out"}>
              <ArrowRightFromSquare />
            </button>
          </div>
        </header>
        <main className="content">
          <PageHeaderContext.Provider value={slots}>{children}</PageHeaderContext.Provider>
        </main>
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

// NavSection groups a handful of NavLinks under a collapsible header -- the
// sidebar outgrew a flat list once every feature area got its own entry (21
// rows, forcing a scrollbar even on a normal window). Grouping mirrors
// permissionGroups in ../permissions.tsx on purpose: an operator who already
// learned that taxonomy from the Operators permission editor finds the same
// categories here, not a second one to learn.
//
// Collapsed sidebar mode (icon rail) renders children directly with no
// header/indent -- an accordion makes no sense once labels are gone anyway,
// and this keeps that mode exactly as flat as it always was.
function NavSection({
  title,
  icon,
  route,
  paths,
  collapsed,
  children
}: {
  title: string;
  icon: ReactNode;
  route: RouteState;
  paths: string[];
  collapsed: boolean;
  children: ReactNode;
}) {
  const active = paths.some((p) => route.path === p || route.path.startsWith(`${p}/`));
  const storageKey = `owpengram.nav.section.${title}`;
  const [open, setOpen] = useState(() => {
    try {
      const stored = localStorage.getItem(storageKey);
      if (stored !== null) return stored === "1";
    } catch {
      // Falls through to the default below.
    }
    return active;
  });

  // Landing on a route inside a collapsed section (a bookmark, a link from
  // elsewhere) opens it -- an operator should not have to separately find
  // "expand" before seeing where they already are.
  useEffect(() => {
    if (active) setOpen(true);
  }, [active]);

  function toggle() {
    setOpen((current) => {
      const next = !current;
      try {
        localStorage.setItem(storageKey, next ? "1" : "0");
      } catch {
        // Not being able to remember the choice is not a reason to refuse it.
      }
      return next;
    });
  }

  if (collapsed) {
    return <>{children}</>;
  }

  return (
    <div className={`nav-section ${active ? "active" : ""} ${open ? "open" : ""}`.trim()}>
      <button type="button" className="nav-section-toggle" onClick={toggle} aria-expanded={open}>
        {icon}
        <span>{title}</span>
        <ChevronDown width={12} height={12} className="nav-section-chevron" aria-hidden="true" />
      </button>
      {open && <div className="nav-children">{children}</div>}
    </div>
  );
}
