import { APIError, csrfToken } from "../api";

// Client for the private-content viewer (cmd/telesrv-admin/privateview.go).
// Every call carries the view grant the password re-check returned; the grant
// lives only in the page that asked for it.

export type ViewKind = "user" | "channel";

export type ChatMedia = {
  kind: string;
  thumb?: string;
  full?: string;
  w?: number;
  h?: number;
  duration?: number;
  mime_type?: string;
  file_name?: string;
  size?: number;
  title?: string;
  subtitle?: string;
  spoiler?: boolean;
  animated?: boolean;
};

export type ChatMessage = {
  id: number;
  date: number;
  edit_date?: number;
  from_type: "user" | "channel";
  from_id: number;
  out?: boolean;
  text: string;
  deleted?: boolean;
  pinned?: boolean;
  post?: boolean;
  post_author?: string;
  views?: number;
  reply_to_id?: number;
  fwd_from_type?: string;
  fwd_from_id?: number;
  fwd_from_name?: string;
  via_bot_id?: number;
  media?: ChatMedia;
  service?: string;
  comments?: number;
  has_thread?: boolean;
};

export type ChatReply = { from_type: string; from_id: number; text: string; media?: string };
export type ChatPeer = { name: string; username?: string };

export type ChatPage = {
  messages: ChatMessage[];
  replies: Record<string, ChatReply>;
  peers: Record<string, ChatPeer>;
  has_more: boolean;
};

export type ChatListItem = {
  kind: "user" | "group" | "channel";
  peer_id: number;
  title: string;
  username: string;
  bot?: boolean;
  self?: boolean;
  role?: string;
  status?: string;
  unread?: number;
  pinned?: boolean;
  last_date: number;
  last_text: string;
  last_media?: string;
  last_outgoing?: boolean;
};

export type ProfilePhoto = { photo_id: number; date: number; kind: string; active: boolean; thumb?: string; full?: string; video?: string };
export type ProfileStory = {
  id: number;
  date: number;
  expire_date: number;
  deleted?: boolean;
  pinned?: boolean;
  public?: boolean;
  caption?: string;
  media?: ChatMedia;
};
export type ProfileGift = {
  id: number;
  gift_id: number;
  from_user_id: number;
  from_name?: string;
  date: number;
  title: string;
  stars: number;
  message?: string;
  hidden?: boolean;
  converted?: boolean;
  name_hidden?: boolean;
  unique?: boolean;
  unique_name?: string;
};
export type ProfileContent = { photos: ProfilePhoto[]; stories: ProfileStory[]; gifts: ProfileGift[] };
export type MediaItem = { peer_id: number; id: number; date: number; out?: boolean; media: ChatMedia };

export type Cursor = { beforeDate: number; beforeID: number } | null;

// VIEW_LOCKED means the grant expired or was never valid: the page goes back
// to asking for the password.
export function isViewLocked(err: unknown): boolean {
  return err instanceof APIError && err.status === 403 && err.message === "VIEW_LOCKED";
}

async function call<T>(url: string, grant: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(url, {
    credentials: "same-origin",
    cache: "no-store",
    ...init,
    headers: {
      "Content-Type": "application/json",
      "X-View-Grant": grant,
      ...(init.method && init.method !== "GET" ? { "X-CSRF-Token": csrfToken() } : {}),
      ...(init.headers as Record<string, string> | undefined)
    }
  });
  const text = await response.text();
  const data = text ? JSON.parse(text) : null;
  if (!response.ok) {
    throw new APIError(response.status, data?.error || response.statusText);
  }
  return data as T;
}

function withCursor(base: string, cursor: Cursor): string {
  if (!cursor) return base;
  const sep = base.includes("?") ? "&" : "?";
  return `${base}${sep}before_date=${cursor.beforeDate}&before_id=${cursor.beforeID}`;
}

export const privateView = {
  unlock: (kind: ViewKind, id: number, password: string, reason: string) =>
    call<{ grant: string; expires_at: string }>("/api/private/unlock", "", {
      method: "POST",
      body: JSON.stringify({ kind, id, password, reason })
    }),
  userChats: (userID: number, grant: string) => call<{ chats: ChatListItem[] }>(`/api/private/users/${userID}/chats`, grant),
  userChat: (userID: number, kind: "user" | "channel", peerID: number, cursor: Cursor, grant: string) =>
    call<ChatPage>(withCursor(`/api/private/users/${userID}/chats/${kind}/${peerID}`, cursor), grant),
  userMedia: (userID: number, cursor: Cursor, grant: string) =>
    call<{ items: MediaItem[]; has_more: boolean }>(withCursor(`/api/private/users/${userID}/media`, cursor), grant),
  profile: (kind: ViewKind, id: number, grant: string) =>
    call<ProfileContent>(`/api/private/${kind === "user" ? "users" : "channels"}/${id}/profile`, grant),
  channelMessages: (channelID: number, cursor: Cursor, grant: string) =>
    call<ChatPage>(withCursor(`/api/private/channels/${channelID}/messages`, cursor), grant),
  channelComments: (channelID: number, postID: number, cursor: Cursor, grant: string) =>
    call<{ group_id: number; page: ChatPage }>(withCursor(`/api/private/channels/${channelID}/comments?post_id=${postID}`, cursor), grant),
  mediaURL: (key: string, grant: string, lottie = false) =>
    `/api/private/media?key=${encodeURIComponent(key)}&grant=${encodeURIComponent(grant)}${lottie ? "&lottie=1" : ""}`
};

export function cursorAfter(page: ChatPage): Cursor {
  const last = page.messages[page.messages.length - 1];
  return last ? { beforeDate: last.date, beforeID: last.id } : null;
}
