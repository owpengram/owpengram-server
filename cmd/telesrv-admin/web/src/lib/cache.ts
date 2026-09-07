// Last-known values for screens that are visited repeatedly.
//
// Navigating back to the dashboard used to re-mount it with empty state, so an
// operator watched the same numbers redraw from skeletons every time. The
// screens already refresh themselves; what they lacked was something to show
// while that happens. Reading from here on mount means the page opens on the
// figures it had, and the refresh quietly replaces them.
//
// Kept in module memory rather than localStorage on purpose. This is admin data
// -- account counts, storage figures -- and it has no business outliving the
// tab or sitting on disk. It dies on reload, and clearAdminCache() drops it at
// sign-out so the next operator in the same tab never sees the previous one's
// figures.

const store = new Map<string, unknown>();

export function cacheGet<T>(key: string): T | undefined {
  return store.get(key) as T | undefined;
}

export function cacheSet<T>(key: string, value: T): void {
  store.set(key, value);
}

export function clearAdminCache(): void {
  store.clear();
}

// Keys live here rather than as loose strings at each call site, so a typo
// cannot quietly create a second cache that never hits.
export const cacheKeys = {
  dashboard: "dashboard",
  storageStats: "storage.stats",
  storageAccounts: "storage.accounts"
} as const;
