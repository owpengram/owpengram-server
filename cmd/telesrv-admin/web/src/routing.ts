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

export function routeTitle(pathname: string): string {
  // Third-party verification is tested before the official section and before
  // "/bots": three different prefixes that all read as "verification of a bot".
  if (pathname.startsWith("/bot-verification")) return "Third-party verification";
  if (pathname.startsWith("/verification")) return "Official Verification";
  if (pathname.startsWith("/collectible-usernames")) return "Collectible Usernames";
  if (pathname.startsWith("/storage")) return "Storage";
  if (pathname.startsWith("/accounts/shared-devices")) return "Shared Devices";
  if (pathname.startsWith("/accounts")) return "Accounts";
  if (pathname.startsWith("/channels")) return "Supergroups and Channels";
  if (pathname.startsWith("/bots")) return "Bots";
  if (pathname.startsWith("/moderation")) return "Reports and Moderation";
  if (pathname.startsWith("/broadcasts")) return "Broadcasts";
  if (pathname.startsWith("/emoji")) return "Emoji";
  if (pathname.startsWith("/messages")) return "Message Audit";
	if (pathname.startsWith("/stickers")) return "Stickers";
  if (pathname.startsWith("/gif-catalog")) return "GIFs";
  if (pathname.startsWith("/server-settings")) return "Server Settings";
  return "Operations Console";
}
