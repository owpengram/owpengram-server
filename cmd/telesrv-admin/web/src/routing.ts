export type Navigate = (href: string) => void;

export type RouteState = {
  href: string;
  path: string;
  search: URLSearchParams;
};

export function currentRoute(): RouteState {
  return {
    href: `${window.location.pathname}${window.location.search}`,
    path: window.location.pathname,
    search: new URLSearchParams(window.location.search)
  };
}

// ROUTES is the single list behind the sidebar group, the top-bar title and
// the command palette, so a section reads the same everywhere it is named.
// Order matters for prefix matching: a longer prefix must come before a
// shorter one it extends ("/accounts/shared-devices" before "/accounts").
export const ROUTES: { prefix: string; title: string; section: string }[] = [
  { prefix: "/bot-verification", title: "Third-party marks", section: "Moderation" },
  { prefix: "/verification", title: "Verification", section: "Moderation" },
  { prefix: "/moderation", title: "Reports", section: "Moderation" },
  { prefix: "/accounts/shared-devices", title: "Shared devices", section: "Users and chats" },
  { prefix: "/accounts", title: "Accounts", section: "Users and chats" },
  { prefix: "/channels", title: "Groups and channels", section: "Users and chats" },
  { prefix: "/messages", title: "Messages", section: "Users and chats" },
  { prefix: "/bots", title: "Bots", section: "Users and chats" },
  { prefix: "/broadcasts", title: "Broadcasts", section: "Users and chats" },
  { prefix: "/stickers", title: "Stickers", section: "Content" },
  { prefix: "/emoji", title: "Emoji", section: "Content" },
  { prefix: "/gif-catalog", title: "GIFs", section: "Content" },
  { prefix: "/premium", title: "Premium", section: "Economy" },
  { prefix: "/donations", title: "Donations", section: "Economy" },
  { prefix: "/account-ratings", title: "Ratings", section: "Economy" },
  { prefix: "/star-gift-catalog", title: "Gift catalog", section: "Economy" },
  { prefix: "/give-gifts", title: "Give gifts", section: "Economy" },
  { prefix: "/collectible-usernames", title: "Usernames", section: "Economy" },
  { prefix: "/storage", title: "Storage", section: "Server" },
  { prefix: "/admin-users", title: "Operators", section: "Server" },
  { prefix: "/audit-log", title: "Audit log", section: "Server" },
  { prefix: "/server-settings", title: "Settings", section: "Server" }
];

function routeEntry(pathname: string) {
  return ROUTES.find((r) => pathname === r.prefix || pathname.startsWith(`${r.prefix}/`) || pathname.startsWith(r.prefix));
}

export function routeTitle(pathname: string): string {
  return routeEntry(pathname)?.title ?? "Overview";
}

// routeSection is the sidebar group a page lives in, shown before its title
// in the top bar. Overview has none.
export function routeSection(pathname: string): string {
  return routeEntry(pathname)?.section ?? "";
}
