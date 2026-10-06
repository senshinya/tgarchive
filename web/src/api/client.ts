import type {
  AddBotResult,
  Article,
  Bot,
  ChannelInfo,
  Chat,
  CondGroup,
  Downloads,
  Message,
  RejectedSender,
  SharedMediaType,
  TelegramApp,
  UserbotInfo,
  Watch,
  WatchInput,
  WatchTestResult,
  WhitelistEntry,
} from './types';

export const PAGE_SIZE = 50;

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
  messages(chatId: number, before?: number, limit?: number): Promise<Message[]>;
  message(id: number): Promise<Message>;
  article(messageId: number): Promise<Article>;
  chatMedia(chatId: number, type: SharedMediaType, before?: number, limit?: number): Promise<Message[]>;
  /** A bot's merged timeline: every chat of the bot. */
  botMessages(botId: number, before?: number, limit?: number): Promise<Message[]>;
  botMedia(botId: number, type: SharedMediaType, before?: number, limit?: number): Promise<Message[]>;
  deleteMessage(id: number): Promise<void>;
  retryMedia(id: number): Promise<void>;
  downloads(): Promise<Downloads>;
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
  channels(refresh?: boolean): Promise<ChannelInfo[]>;
  searchChannels(q: string): Promise<ChannelInfo[]>;
  resolveChannel(input: string): Promise<ChannelInfo>;
  /** Judges a channel's latest posts against a draft condition (null: none yet). */
  testWatch(channelId: number, cond: CondGroup | null): Promise<WatchTestResult>;
  watches(): Promise<Watch[]>;
  watch(id: number): Promise<Watch>;
  createWatch(input: WatchInput): Promise<Watch>;
  updateWatch(id: number, input: WatchInput): Promise<Watch>;
  deleteWatch(id: number, purge: boolean): Promise<void>;
  watchSettings(): Promise<{ poll_seconds: number }>;
  saveWatchSettings(pollSeconds: number): Promise<{ poll_seconds: number }>;
}

export const api: Api = {
  bots: () => request('GET', '/api/bots'),
  chats: () => request('GET', '/api/chats'),
  messages: (chatId, before = 0, limit = PAGE_SIZE) =>
    request('GET', `/api/chats/${chatId}/messages${qs({ before, limit })}`),
  message: (id) => request('GET', `/api/messages/${id}`),
  article: (messageId) => request('GET', `/api/messages/${messageId}/article`),
  chatMedia: (chatId, type, before = 0, limit = PAGE_SIZE) =>
    request('GET', `/api/chats/${chatId}/media${qs({ type, before, limit })}`),
  botMessages: (botId, before = 0, limit = PAGE_SIZE) =>
    request('GET', `/api/bots/${botId}/messages${qs({ before, limit })}`),
  botMedia: (botId, type, before = 0, limit = PAGE_SIZE) =>
    request('GET', `/api/bots/${botId}/media${qs({ type, before, limit })}`),
  deleteMessage: (id) => request('DELETE', `/api/messages/${id}`),
  retryMedia: (id) => request('POST', `/api/media/${id}/retry`),
  downloads: () => request('GET', '/api/downloads'),
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
  watchSettings: () => request('GET', '/api/admin/watch-settings'),
  saveWatchSettings: (pollSeconds) => request('PUT', '/api/admin/watch-settings', { poll_seconds: pollSeconds }),
};

/** A conversation's messages: a chat for a positive key, a bot's merged timeline for -botId. */
export function convMessages(api: Api, key: number, before = 0, limit = PAGE_SIZE): Promise<Message[]> {
  return key < 0 ? api.botMessages(-key, before, limit) : api.messages(key, before, limit);
}

/** A conversation's shared media; keys as in convMessages. */
export function convMedia(api: Api, key: number, type: SharedMediaType, before = 0, limit = PAGE_SIZE): Promise<Message[]> {
  return key < 0 ? api.botMedia(-key, type, before, limit) : api.chatMedia(key, type, before, limit);
}

export function mediaUrl(id: number, download = false): string {
  return `/media/${id}${download ? '?download=1' : ''}`;
}

export function avatarUrl(kind: 'bots' | 'senders' | 'channels', tgId: number): string {
  return `/avatars/${kind}/${tgId}`;
}

export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof Error) return err.message;
  return String(err);
}
