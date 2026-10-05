import { vi } from 'vitest';
import type { Api } from '../api/client';
import type { Article, ArticleMedia, Bot, Chat, Media, Message } from '../api/types';

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
    bot_id: 1,
    sender: { tg_user_id: 42, first_name: 'Alice', last_name: '', username: 'alice', has_avatar: false },
    last_message_at: 1_790_000_000,
    last_kind: 'text',
    last_text: 'hello',
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
  };
  return { ...base, ...over };
}
