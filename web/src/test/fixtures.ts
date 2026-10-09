import { vi } from 'vitest';
import type { Api } from '../api/client';
import type { Article, ArticleMedia, Bot, ChannelInfo, Chat, Media, Message, Stats, Watch } from '../api/types';

export function makeBot(over: Partial<Bot> = {}): Bot {
  return {
    id: 1,
    tg_bot_id: 777,
    username: 'archive_bot',
    name: 'Archive',
    has_avatar: false,
    enabled: true,
    status: 'running',
    last_error: '',
    ...over,
  };
}

export function makeChat(over: Partial<Chat> = {}): Chat {
  return {
    id: 10,
    kind: 'private',
    bot_id: 1,
    sender: { tg_user_id: 42, first_name: 'Alice', last_name: '', username: 'alice', has_avatar: false },
    channel: null,
    watch: null,
    last_message_at: 1_790_000_000,
    last_kind: 'text',
    last_text: 'hello',
    last_read_pos: 0,
    unread: 0,
    first_unread_id: 0,
    ...over,
  };
}

/** A watched channel's conversation. */
export function makeChannelChat(over: Partial<Chat> = {}): Chat {
  return makeChat({
    id: 50,
    kind: 'channel',
    bot_id: 0,
    sender: { tg_user_id: 0, first_name: '', last_name: '', username: '', has_avatar: false },
    channel: { channel_id: 500, title: 'News', username: 'news', has_avatar: false },
    watch: { id: 3, enabled: true, status: 'ok', error: '', window_minutes: 30, pending: 2, hits: 5 },
    ...over,
  });
}

export function makeChannelInfo(over: Partial<ChannelInfo> = {}): ChannelInfo {
  return { channel_id: 500, title: 'News', username: 'news', participants: 1200, watched: false, ...over };
}

export function makeStats(over: Partial<Stats> = {}): Stats {
  return {
    totals: {
      messages: 0,
      private_chats: 0,
      channel_chats: 0,
      media_files: 0,
      media_bytes: 0,
      db_bytes: 0,
      disk_free: 0,
      disk_total: 0,
    },
    daily: [],
    monthly: [],
    top_chats: [],
    media_kinds: [],
    media_states: { done: 0, pending: 0, failed: 0, too_large: 0 },
    watches: [],
    ...over,
  };
}

export function makeWatch(over: Partial<Watch> = {}): Watch {
  return {
    id: 3,
    channel: { channel_id: 500, title: 'News', username: 'news', has_avatar: false },
    chat_id: 50,
    window_minutes: 30,
    cond: { op: 'and', items: [{ metric: 'reaction', key: '🔥', cmp: 'gte', value: 10 }] },
    enabled: true,
    status: 'ok',
    error: '',
    pending: 2,
    hits: 5,
    created_at: 1_790_000_000,
    updated_at: 1_790_000_000,
    last_polled_at: 0,
    hits_24h: 0,
    hits_7d: 0,
    last_hit_at: 0,
    poll_seconds: 60,
    backfill: null,
    ...over,
  };
}

export function makeMedia(over: Partial<Media> = {}): Media {
  return {
    id: 100,
    role: 'main',
    kind: 'photo',
    mime: 'image/jpeg',
    file_name: '',
    size: 1000,
    width: 800,
    height: 600,
    duration: 0,
    state: 'done',
    error: '',
    ...over,
  };
}

export function makeMessage(over: Partial<Message> = {}): Message {
  return {
    pos: over.id ?? 1,
    id: 1,
    chat_id: 10,
    tg_message_id: 1,
    source: 'bot_update',
    media_group_id: '',
    date: 1_790_000_000,
    edit_date: 0,
    kind: 'text',
    text: 'hello',
    entities: [],
    reply_to_tg_message_id: 0,
    origin_chat_title: '',
    origin_link: '',
    media: [],
    ...over,
  };
}

export function makeArticleMedia(over: Partial<ArticleMedia> = {}): ArticleMedia {
  return { id: 200, kind: 'photo', state: 'done', width: 0, height: 0, duration: 0, mime: 'image/jpeg', ...over };
}

export function makeArticle(over: Partial<Article> = {}): Article {
  return {
    url: 'https://telegra.ph/Sample-10-05',
    title: 'Sample',
    description: 'A sample article',
    author_name: 'Anon',
    author_url: 'https://t.me/anon',
    views: 7,
    fetched_at: 1_790_000_000,
    content: [{ tag: 'p', children: ['Hello'] }],
    media: [],
    ...over,
  };
}

/** An Api whose every method is a vi.fn with an empty-but-valid default result. */
export function fakeApi(over: Partial<Api> = {}): Api {
  const base: Api = {
    bots: vi.fn(async () => []),
    chats: vi.fn(async () => []),
    messages: vi.fn(async () => []),
    message: vi.fn(async (id: number) => makeMessage({ id })),
    article: vi.fn(async () => makeArticle()),
    chatMedia: vi.fn(async () => []),
    botMessages: vi.fn(async () => []),
    botMedia: vi.fn(async () => []),
    deleteMessage: vi.fn(async () => undefined),
    retryMedia: vi.fn(async () => undefined),
    downloads: vi.fn(async () => ({ active: [], queued: { count: 0, bytes: 0 }, failed: [], speed: 0 })),
    search: vi.fn(async () => ({ items: [], next: 0 })),
    favorite: vi.fn(async () => ({ at: 1, tags: [] })),
    unfavorite: vi.fn(async () => undefined),
    setTags: vi.fn(async (_id: number, tags: string[]) => ({ tags: tags.map((name, i) => ({ id: i + 1, name })) })),
    favorites: vi.fn(async () => ({ items: [], next: 0 })),
    tags: vi.fn(async () => []),
    deleteTag: vi.fn(async () => undefined),
    markRead: vi.fn(async () => undefined),
    refreshPostStats: vi.fn(async () => undefined),
    comments: vi.fn(async () => []),
    refreshComments: vi.fn(async () => undefined),
    allMedia: vi.fn(async () => []),
    stats: vi.fn(async () => makeStats()),
    addBot: vi.fn(async () => ({ bot_id: 1, steps: [] })),
    setBotEnabled: vi.fn(async (id: number, enabled: boolean) => makeBot({ id, enabled })),
    deleteBot: vi.fn(async () => undefined),
    whitelist: vi.fn(async () => []),
    putWhitelist: vi.fn(async () => undefined),
    deleteWhitelist: vi.fn(async () => undefined),
    rejected: vi.fn(async () => []),
    telegramApp: vi.fn(async () => ({ configured: true, api_id: 1, server: { managed: true, state: 'running', error: '' } })),
    saveTelegramApp: vi.fn(async () => undefined),
    userbot: vi.fn(async () => ({ state: 'logged_out' as const, phone: '', name: '', tg_user_id: 0, error: '' })),
    userbotPhone: vi.fn(async () => ({ state: 'code_sent' as const, phone: '+1', name: '', tg_user_id: 0, error: '' })),
    userbotCode: vi.fn(async () => ({ state: 'ready' as const, phone: '+1', name: 'Me', tg_user_id: 5, error: '' })),
    userbotPassword: vi.fn(async () => ({ state: 'ready' as const, phone: '+1', name: 'Me', tg_user_id: 5, error: '' })),
    userbotLogout: vi.fn(async () => undefined),
    channels: vi.fn(async () => ({ channels: [], loading: false, updated_at: 1, error: '' })),
    searchChannels: vi.fn(async () => []),
    resolveChannel: vi.fn(async () => makeChannelInfo()),
    testWatch: vi.fn(async () => ({ posts: [], reactions_available: { all: false, list: [] } })),
    watches: vi.fn(async () => []),
    watch: vi.fn(async (id: number) => makeWatch({ id })),
    createWatch: vi.fn(async () => makeWatch()),
    updateWatch: vi.fn(async (id: number) => makeWatch({ id })),
    deleteWatch: vi.fn(async () => undefined),
    backfillWatch: vi.fn(async (_id: number, hours: number) => ({ running: true, hours, scanned: 0, archived: 0, error: '', started_at: 1, finished_at: 0 })),
    watchSettings: vi.fn(async () => ({ poll_seconds: 60 })),
    saveWatchSettings: vi.fn(async (poll_seconds: number) => ({ poll_seconds })),
  };
  return { ...base, ...over };
}
