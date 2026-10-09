import type {
  AddBotResult,
  Article,
  Bot,
  BackfillState,
  ChannelInfo,
  ChannelList,
  Chat,
  CondGroup,
  Downloads,
  FavoriteInfo,
  FavoritesPage,
  Message,
  RejectedSender,
  SearchPage,
  Tag,
  TagCount,
  SharedMediaType,
  Stats,
  TelegramApp,
  UserbotInfo,
  Watch,
  WatchInput,
  WatchTestResult,
  WallSource,
  WallType,
  WhitelistEntry,
} from './types';

export const PAGE_SIZE = 50;
/** Media wall page size. */
export const WALL_PAGE = 60;

/** A conversation page other than "older than": newer than a message, or a window around one. */
export interface PageParams {
  after?: number;
  around?: number;
}

export class ApiError extends Error {
  readonly status: number;
  readonly body: unknown;

  constructor(status: number, message: string, body: unknown) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.body = body;
  }
}

export const NETWORK_ERROR = '网络错误或登录已过期，请刷新页面';

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, credentials: 'same-origin', redirect: 'error' };
  if (body !== undefined) {
    init.headers = { 'Content-Type': 'application/json' };
    init.body = JSON.stringify(body);
  }
  let res: Response;
  try {
    res = await fetch(path, init);
  } catch {
    throw new ApiError(0, NETWORK_ERROR, undefined);
  }
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  let data: unknown;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = text;
    }
  }
  if (!res.ok) {
    if (res.status === 401) throw new ApiError(401, NETWORK_ERROR, data);
    const msg =
      data && typeof data === 'object' && 'error' in data ? String((data as { error: unknown }).error) : `HTTP ${res.status}`;
    throw new ApiError(res.status, msg, data);
  }
  return data as T;
}

function qs(params: Record<string, string | number | undefined>): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== 0 && v !== '') p.set(k, String(v));
  }
  const s = p.toString();
  return s ? `?${s}` : '';
}

export interface Api {
  bots(): Promise<Bot[]>;
  chats(): Promise<Chat[]>;
  /** A chat's messages: older than `before` (the latest with 0), or per `page` newer than a
   * message or a window around it. */
  messages(chatId: number, before?: number, limit?: number, page?: PageParams): Promise<Message[]>;
  message(id: number): Promise<Message>;
  article(messageId: number): Promise<Article>;
  chatMedia(chatId: number, type: SharedMediaType, before?: number, limit?: number): Promise<Message[]>;
  /** A bot's merged timeline: every chat of the bot. */
  botMessages(botId: number, before?: number, limit?: number, page?: PageParams): Promise<Message[]>;
  botMedia(botId: number, type: SharedMediaType, before?: number, limit?: number): Promise<Message[]>;
  deleteMessage(id: number): Promise<void>;
  retryMedia(id: number): Promise<void>;
  downloads(): Promise<Downloads>;
  /** Messages containing every word of q; chat limits it to a conversation key (see convMessages). */
  search(q: string, chat?: number, before?: number): Promise<SearchPage>;
  /** Adds a message to the favorites; tags, when given, replace its tags. */
  favorite(id: number, tags?: string[]): Promise<FavoriteInfo>;
  unfavorite(id: number): Promise<void>;
  setTags(id: number, tags: string[]): Promise<{ tags: Tag[] }>;
  /** The favorites, most recently added first; tag 0 means any. */
  favorites(tag?: number, before?: number): Promise<FavoritesPage>;
  tags(): Promise<TagCount[]>;
  deleteTag(id: number): Promise<void>;
  /** Records that a chat has been read up to a message. */
  markRead(chatId: number, messageId: number): Promise<void>;
  /** Asks for fresh counters of a channel conversation's archived posts (they arrive as message.updated). */
  refreshPostStats(chatId: number, messageIds: number[]): Promise<void>;
  /** The comments of an archived post: per `page`, or the newest. */
  comments(chatId: number, postId: number, page?: PageParams, limit?: number): Promise<Message[]>;
  /** Asks for an archived post's new comments (they arrive as comments.updated). */
  refreshComments(chatId: number, postId: number): Promise<void>;
  addBot(token: string): Promise<AddBotResult>;
  setBotEnabled(id: number, enabled: boolean): Promise<Bot>;
  deleteBot(id: number, purge: boolean): Promise<void>;
  whitelist(botId: number): Promise<WhitelistEntry[]>;
  putWhitelist(botId: number, uid: number, note: string, canFetch: boolean): Promise<void>;
  deleteWhitelist(botId: number, uid: number): Promise<void>;
  rejected(botId: number): Promise<RejectedSender[]>;
  telegramApp(): Promise<TelegramApp>;
  saveTelegramApp(apiId: number, apiHash: string): Promise<void>;
  userbot(): Promise<UserbotInfo>;
  userbotPhone(phone: string): Promise<UserbotInfo>;
  userbotCode(code: string): Promise<UserbotInfo>;
  userbotPassword(password: string): Promise<UserbotInfo>;
  userbotLogout(): Promise<void>;
  /** Broadcast channels the user account has joined; refresh bypasses the server's cache. */
  channels(refresh?: boolean): Promise<ChannelList>;
  searchChannels(q: string): Promise<ChannelInfo[]>;
  resolveChannel(input: string): Promise<ChannelInfo>;
  /** Judges a channel's latest posts against a draft condition (null: none yet). */
  testWatch(channelId: number, cond: CondGroup | null): Promise<WatchTestResult>;
  watches(): Promise<Watch[]>;
  watch(id: number): Promise<Watch>;
  createWatch(input: WatchInput): Promise<Watch>;
  updateWatch(id: number, input: WatchInput): Promise<Watch>;
  deleteWatch(id: number, purge: boolean): Promise<void>;
  /** Judges the channel's posts of the last `hours` by their current counts, in the background. */
  backfillWatch(id: number, hours: number): Promise<BackfillState>;
  /** The media wall: every chat's photos, videos and GIFs, newest first. */
  allMedia(type: WallType, source: WallSource, before?: number, limit?: number): Promise<Message[]>;
  /** Archive statistics, days cut in the time zone tz minutes east of UTC. */
  stats(tz: number): Promise<Stats>;
  watchSettings(): Promise<{ poll_seconds: number }>;
  saveWatchSettings(pollSeconds: number): Promise<{ poll_seconds: number }>;
}

export const api: Api = {
  bots: () => request('GET', '/api/bots'),
  chats: () => request('GET', '/api/chats'),
  messages: (chatId, before = 0, limit = PAGE_SIZE, page = {}) =>
    request('GET', `/api/chats/${chatId}/messages${qs({ before, limit, ...page })}`),
  message: (id) => request('GET', `/api/messages/${id}`),
  article: (messageId) => request('GET', `/api/messages/${messageId}/article`),
  chatMedia: (chatId, type, before = 0, limit = PAGE_SIZE) =>
    request('GET', `/api/chats/${chatId}/media${qs({ type, before, limit })}`),
  botMessages: (botId, before = 0, limit = PAGE_SIZE, page = {}) =>
    request('GET', `/api/bots/${botId}/messages${qs({ before, limit, ...page })}`),
  botMedia: (botId, type, before = 0, limit = PAGE_SIZE) =>
    request('GET', `/api/bots/${botId}/media${qs({ type, before, limit })}`),
  deleteMessage: (id) => request('DELETE', `/api/messages/${id}`),
  retryMedia: (id) => request('POST', `/api/media/${id}/retry`),
  downloads: () => request('GET', '/api/downloads'),
  search: (q, chat = 0, before = 0) => request('GET', `/api/search${qs({ q, chat, before })}`),
  favorite: (id, tags) => request('PUT', `/api/messages/${id}/favorite`, tags ? { tags } : undefined),
  unfavorite: (id) => request('DELETE', `/api/messages/${id}/favorite`),
  setTags: (id, tags) => request('PUT', `/api/messages/${id}/tags`, { tags }),
  favorites: (tag = 0, before = 0) => request('GET', `/api/favorites${qs({ tag, before })}`),
  tags: () => request('GET', '/api/tags'),
  deleteTag: (id) => request('DELETE', `/api/tags/${id}`),
  allMedia: (type, source, before = 0, limit = WALL_PAGE) => request('GET', `/api/media${qs({ type, source, before, limit })}`),
  stats: (tz) => request('GET', `/api/stats${qs({ tz })}`),
  markRead: (chatId, messageId) => request('POST', `/api/chats/${chatId}/read`, { message_id: messageId }),
  refreshPostStats: (chatId, messageIds) => request('POST', `/api/chats/${chatId}/refresh-stats`, { message_ids: messageIds }),
  comments: (chatId, postId, page = {}, limit = PAGE_SIZE) =>
    request('GET', `/api/chats/${chatId}/posts/${postId}/comments${qs({ limit, ...page })}`),
  refreshComments: (chatId, postId) => request('POST', `/api/chats/${chatId}/posts/${postId}/refresh-comments`),
  addBot: (token) => request('POST', '/api/admin/bots', { token }),
  setBotEnabled: (id, enabled) => request('PATCH', `/api/admin/bots/${id}`, { enabled }),
  deleteBot: (id, purge) => request('DELETE', `/api/admin/bots/${id}${purge ? '?purge=1' : ''}`),
  whitelist: (botId) => request('GET', `/api/admin/bots/${botId}/whitelist`),
  putWhitelist: (botId, uid, note, canFetch) =>
    request('PUT', `/api/admin/bots/${botId}/whitelist/${uid}`, { note, can_fetch: canFetch }),
  deleteWhitelist: (botId, uid) => request('DELETE', `/api/admin/bots/${botId}/whitelist/${uid}`),
  rejected: (botId) => request('GET', `/api/admin/bots/${botId}/rejected`),
  telegramApp: () => request('GET', '/api/admin/telegram-app'),
  saveTelegramApp: (apiId, apiHash) => request('PUT', '/api/admin/telegram-app', { api_id: apiId, api_hash: apiHash }),
  userbot: () => request('GET', '/api/admin/userbot'),
  userbotPhone: (phone) => request('POST', '/api/admin/userbot/phone', { phone }),
  userbotCode: (code) => request('POST', '/api/admin/userbot/code', { code }),
  userbotPassword: (password) => request('POST', '/api/admin/userbot/password', { password }),
  userbotLogout: () => request('POST', '/api/admin/userbot/logout'),
  channels: (refresh = false) => request('GET', `/api/admin/channels${refresh ? '?refresh=1' : ''}`),
  searchChannels: (q) => request('GET', `/api/admin/channels/search${qs({ q })}`),
  resolveChannel: (input) => request('POST', '/api/admin/channels/resolve', { input }),
  testWatch: (channelId, cond) => request('POST', '/api/admin/watches/test', { channel_id: channelId, cond }),
  watches: () => request('GET', '/api/admin/watches'),
  watch: (id) => request('GET', `/api/admin/watches/${id}`),
  createWatch: (input) => request('POST', '/api/admin/watches', input),
  updateWatch: (id, input) => request('PUT', `/api/admin/watches/${id}`, input),
  deleteWatch: (id, purge) => request('DELETE', `/api/admin/watches/${id}${purge ? '?purge=1' : ''}`),
  backfillWatch: (id, hours) => request('POST', `/api/admin/watches/${id}/backfill`, { hours }),
  watchSettings: () => request('GET', '/api/admin/watch-settings'),
  saveWatchSettings: (pollSeconds) => request('PUT', '/api/admin/watch-settings', { poll_seconds: pollSeconds }),
};

/** A conversation's messages: a chat for a positive key, a bot's merged timeline for -botId. */
export function convMessages(api: Api, key: number, before = 0, limit = PAGE_SIZE, page?: PageParams): Promise<Message[]> {
  if (page) return key < 0 ? api.botMessages(-key, before, limit, page) : api.messages(key, before, limit, page);
  return key < 0 ? api.botMessages(-key, before, limit) : api.messages(key, before, limit);
}

/** A conversation's shared media; keys as in convMessages. */
export function convMedia(api: Api, key: number, type: SharedMediaType, before = 0, limit = PAGE_SIZE): Promise<Message[]> {
  return key < 0 ? api.botMedia(-key, type, before, limit) : api.chatMedia(key, type, before, limit);
}

export function mediaUrl(id: number, download = false): string {
  return `/media/${id}${download ? '?download=1' : ''}`;
}

export function avatarUrl(kind: 'bots' | 'senders' | 'channels' | 'users', tgId: number): string {
  return `/avatars/${kind}/${tgId}`;
}

export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof Error) return err.message;
  return String(err);
}
