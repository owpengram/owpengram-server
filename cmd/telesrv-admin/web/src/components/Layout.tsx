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
  ShieldAlert,
  ShieldCheck,
  Smile,
  Stamp,
  Users,
	Sticker
} from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { api } from "../api";
import { permissionBotVerificationReview, permissionServerManage, permissionVerificationReview, useCan, useThirdPartyVerificationHidden } from "../permissions";
import { type Navigate, type RouteState, routeTitle } from "../routing";
import { ThemeSwitch } from "../theme";
import { AppLink } from "./AppLink";

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
  build,
  route,
  navigate,
  onLogout,
  children
}: {
  actor: string;
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
          <span className="brand-mark"><img src="/logo.png" alt="OwpenGram" /></span>
          <span>
            <strong>OwpenGram</strong>
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
          {build?.short_commit && (
            <span className="sidebar-label sidebar-build" title={build.commit + (build.dirty ? " (uncommitted changes)" : "")}>
              {`Build: ${build.short_commit}${build.dirty ? "+" : ""}`}
            </span>
          )}
        </div>
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
