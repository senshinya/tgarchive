import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '../api/client';
import type { Message } from '../api/types';
import { fakeApi, makeBot, makeChannelChat, makeChat, makeMedia, makeMessage } from '../test/fixtures';
import { CHANNELS_FILTER, createStore } from './store';

const page = (from: number, to: number, chat = 10): Message[] =>
  Array.from({ length: to - from + 1 }, (_, i) => makeMessage({ id: from + i, chat_id: chat }));

const ids = (ms: Message[]) => ms.map((m) => m.id);

describe('store: channel conversations', () => {
  const setup = async () => {
    const api = fakeApi({
      bots: vi.fn(async () => [makeBot({ id: 1 }), makeBot({ id: 2 })]),
      chats: vi.fn(async () => [
        makeChannelChat({ id: 50, last_message_at: 300 }),
        makeChat({ id: 10, bot_id: 1, last_message_at: 200 }),
        makeChat({ id: 11, bot_id: 2, last_message_at: 400 }),
      ]),
    });
    const s = createStore(api, { chatsReloadDelay: 0 });
    await s.loadBots();
    await s.loadChats();
    return { s, api };
  };

  it('lists channels with everything, under the 频道 tab alone, and never under a bot', async () => {
    const { s } = await setup();
    expect(s.visibleChats.value.map((c) => c.id)).toEqual([50, 10, 11]);
    s.botFilter.value = CHANNELS_FILTER;
    expect(s.visibleChats.value.map((c) => c.id)).toEqual([50]);
    s.botFilter.value = 1;
    expect(s.visibleChats.value.map((c) => c.id)).toEqual([10]);
  });

  it('falls back to 全部 when the 频道 tab has nothing left', async () => {
    const { s, api } = await setup();
    s.botFilter.value = CHANNELS_FILTER;
    api.chats = vi.fn(async () => [makeChat({ id: 10, bot_id: 1 })]);
    await s.loadChats();
    expect(s.effectiveBotFilter.value).toBe(0);
  });

  it('mixes bot timelines and channels by recency in bot mode, keeping channels out of bots', async () => {
    const { s } = await setup();
    expect(s.botModeRows.value.map((r) => (r.kind === 'bot' ? `b${r.entry.bot.id}` : `c${r.chat.id}`))).toEqual(['b2', 'c50', 'b1']);
    expect(s.botEntries.value.find((e) => e.bot.id === 1)?.senders).toBe(1);
    expect(s.botKeyOf(50)).toBe(0);
  });

  it('reloads the chat list on watch.updated', async () => {
    const { s, api } = await setup();
    await s.handleEvent({ type: 'watch.updated', data: { watch_id: 3 } });
    await new Promise((r) => setTimeout(r, 5));
    expect(api.chats).toHaveBeenCalledTimes(2);
  });

  it('files a new channel post into the open channel conversation only', async () => {
    const { s, api } = await setup();
    api.messages = vi.fn(async () => []);
    api.botMessages = vi.fn(async () => []);
    await s.refreshLatest(50);
    await s.refreshLatest(-1);
    api.message = vi.fn(async (id: number) => makeMessage({ id, chat_id: 50, source: 'channel_watch' }));
    await s.handleEvent({ type: 'message.created', data: { chat_id: 50, message_id: 9 } });
    expect(s.conv(50).items.map((m) => m.id)).toEqual([9]);
    expect(s.conv(-1).items).toEqual([]);
  });

  it('files a post archived late where it was published, not at the bottom', async () => {
    const { s, api } = await setup();
    const post = (id: number, pos: number) => makeMessage({ id, pos, chat_id: 50, source: 'channel_watch' });
    // Archived as 1, 2, 3 but published as 100, 300, 200: the server pages them by publishing.
    api.messages = vi.fn(async () => [post(1, 100), post(3, 200), post(2, 300)]);
    await s.refreshLatest(50);
    api.message = vi.fn(async (id: number) => post(id, 150)); // a backfill hit
    await s.handleEvent({ type: 'message.created', data: { chat_id: 50, message_id: 9 } });
    expect(s.conv(50).items.map((m) => m.id)).toEqual([1, 9, 3, 2]);
  });
});

describe('store', () => {
  it('filters chats by the selected bot', async () => {
    const api = fakeApi({
      bots: vi.fn(async () => [makeBot({ id: 1 }), makeBot({ id: 2 })]),
      chats: vi.fn(async () => [makeChat({ id: 1, bot_id: 1 }), makeChat({ id: 2, bot_id: 2 })]),
    });
    const s = createStore(api);
    await s.loadBots();
    await s.loadChats();
    expect(s.visibleChats.value.map((c) => c.id)).toEqual([1, 2]);
    s.botFilter.value = 2;
    expect(s.visibleChats.value.map((c) => c.id)).toEqual([2]);
  });

  it('falls back to showing all chats, and resets the filter, once the filtered bot is gone', async () => {
    const api = fakeApi({
      bots: vi.fn(async () => [makeBot({ id: 1 }), makeBot({ id: 2 })]),
      chats: vi.fn(async () => [makeChat({ id: 10, bot_id: 1 }), makeChat({ id: 11, bot_id: 2 })]),
    });
    const s = createStore(api);
    await s.loadBots();
    await s.loadChats();
    s.botFilter.value = 2;
    expect(s.visibleChats.value.map((c) => c.id)).toEqual([11]);

    // Bot 2 gets purged: a reload no longer returns it or its chats.
    api.bots = vi.fn(async () => [makeBot({ id: 1 })]);
    api.chats = vi.fn(async () => [makeChat({ id: 10, bot_id: 1 })]);
    await s.loadBots();
    await s.loadChats();
    expect(s.botFilter.value).toBe(0);
    expect(s.visibleChats.value.map((c) => c.id)).toEqual([10]);
  });

  it('loads the latest page and then older pages until exhausted', async () => {
    const messages = vi.fn(async (_chat: number, before = 0) => (before === 0 ? page(51, 100) : before === 51 ? page(31, 50) : []));
    const s = createStore(fakeApi({ messages }));
    await s.refreshLatest(10);
    expect(s.conv(10).hasMore).toBe(true);
    await s.loadOlder(10);
    expect(messages).toHaveBeenLastCalledWith(10, 51, 50);
    expect(ids(s.conv(10).items)).toEqual(ids(page(31, 100)));
    expect(s.conv(10).hasMore).toBe(false);
    await s.loadOlder(10);
    expect(messages).toHaveBeenCalledTimes(2);
  });

  it('merges a refreshed latest page that overlaps and restarts when it does not', async () => {
    let latest = page(51, 100);
    const s = createStore(fakeApi({ messages: vi.fn(async () => latest) }));
    await s.refreshLatest(10);
    latest = page(52, 101);
    await s.refreshLatest(10);
    expect(ids(s.conv(10).items)).toEqual(ids(page(51, 101)));
    latest = page(300, 349);
    await s.refreshLatest(10);
    expect(ids(s.conv(10).items)).toEqual(ids(page(300, 349)));
  });

  it('drops messages deleted while disconnected that fall within the refreshed overlap', async () => {
    let latest = page(90, 100);
    const s = createStore(fakeApi({ messages: vi.fn(async () => latest) }));
    await s.refreshLatest(10);
    // Server now omits id 95 (deleted while we were away); the rest of the window is unchanged.
    latest = [...page(90, 94), ...page(96, 100)];
    await s.refreshLatest(10);
    expect(ids(s.conv(10).items)).toEqual(ids([...page(90, 94), ...page(96, 100)]));
  });

  it('records a load error on the conversation', async () => {
    const s = createStore(fakeApi({ messages: vi.fn(async () => Promise.reject(new ApiError(500, 'boom', null))) }));
    await s.refreshLatest(10);
    expect(s.conv(10)).toMatchObject({ loading: false, loaded: false, error: 'boom' });
  });

  it('appends created messages to a loaded chat and reloads the chat list', async () => {
    vi.useFakeTimers();
    const api = fakeApi({ messages: vi.fn(async () => page(1, 3)), message: vi.fn(async (id: number) => makeMessage({ id, text: 'new' })) });
    const s = createStore(api, { chatsReloadDelay: 10 });
    await s.refreshLatest(10);
    await s.handleEvent({ type: 'message.created', data: { chat_id: 10, message_id: 4 } });
    await s.handleEvent({ type: 'message.created', data: { chat_id: 99, message_id: 5 } });
    expect(ids(s.conv(10).items)).toEqual([1, 2, 3, 4]);
    expect(api.message).toHaveBeenCalledTimes(1); // chat 99 is not open
    await vi.advanceTimersByTimeAsync(20);
    expect(api.chats).toHaveBeenCalledTimes(1); // debounced
    vi.useRealTimers();
  });

  it('removes deleted messages and drops updated ones that 404', async () => {
    const api = fakeApi({
      messages: vi.fn(async () => page(1, 3)),
      message: vi.fn(async () => Promise.reject(new ApiError(404, 'not found', null))),
    });
    const s = createStore(api, { chatsReloadDelay: 0 });
    await s.refreshLatest(10);
    await s.handleEvent({ type: 'message.deleted', data: { chat_id: 10, message_id: 2 } });
    await s.handleEvent({ type: 'message.updated', data: { chat_id: 10, message_id: 3 } });
    expect(ids(s.conv(10).items)).toEqual([1]);
  });

  it('refreshes only loaded messages on media.updated and tolerates null ids', async () => {
    const api = fakeApi({
      messages: vi.fn(async () => [makeMessage({ id: 1, media: [makeMedia({ id: 7, state: 'pending' })] })]),
      message: vi.fn(async (id: number) => makeMessage({ id, media: [makeMedia({ id: 7, state: 'done' })] })),
    });
    const s = createStore(api);
    await s.refreshLatest(10);
    await s.handleEvent({ type: 'media.updated', data: { media_id: 7, message_ids: [1, 555] } });
    await s.handleEvent({ type: 'media.updated', data: { media_id: 7, message_ids: null } });
    expect(api.message).toHaveBeenCalledTimes(1);
    expect(s.conv(10).items[0].media[0].state).toBe('done');
  });

  it('does not insert an edited message older than the loaded window', async () => {
    const api = fakeApi({ messages: vi.fn(async () => page(51, 100)), message: vi.fn(async (id: number) => makeMessage({ id })) });
    const s = createStore(api);
    await s.refreshLatest(10);
    await s.handleEvent({ type: 'message.updated', data: { chat_id: 10, message_id: 7 } });
    expect(s.conv(10).items[0].id).toBe(51);
  });

  it('logs non-404 refreshMessage errors instead of swallowing them', async () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {});
    const api = fakeApi({
      messages: vi.fn(async () => page(1, 3)),
      message: vi.fn(async () => Promise.reject(new ApiError(500, 'boom', null))),
    });
    const s = createStore(api);
    await s.refreshLatest(10);
    await s.handleEvent({ type: 'message.updated', data: { chat_id: 10, message_id: 3 } });
    expect(spy).toHaveBeenCalled();
    expect(ids(s.conv(10).items)).toEqual([1, 2, 3]); // a non-404 error must not remove the message
    spy.mockRestore();
  });

  it('applies bot.status and reloads bots it does not know', async () => {
    const api = fakeApi({ bots: vi.fn(async () => [makeBot({ id: 1 })]) });
    const s = createStore(api);
    await s.loadBots();
    await s.handleEvent({ type: 'bot.status', data: { bot_id: 1, status: 'error', error: '401 Unauthorized' } });
    expect(s.bots.value[0]).toMatchObject({ status: 'error', last_error: '401 Unauthorized' });
    await s.handleEvent({ type: 'bot.status', data: { bot_id: 2, status: 'running', error: '' } });
    expect(api.bots).toHaveBeenCalledTimes(2);
  });

  it('deletes messages, treating 404 as already gone, and rethrows other errors', async () => {
    const del = vi.fn(async (id: number) => {
      if (id === 2) throw new ApiError(404, 'not found', null);
      if (id === 3) throw new ApiError(500, 'disk', null);
    });
    const s = createStore(fakeApi({ messages: vi.fn(async () => page(1, 3)), deleteMessage: del }), { chatsReloadDelay: 0 });
    await s.refreshLatest(10);
    await s.deleteMessage(makeMessage({ id: 1 }));
    await s.deleteMessage(makeMessage({ id: 2 }));
    await expect(s.deleteMessage(makeMessage({ id: 3 }))).rejects.toThrow('disk');
    expect(ids(s.conv(10).items)).toEqual([3]);
  });

  it('marks retried media pending everywhere it appears, or toasts and refetches on failure', async () => {
    const failed = makeMedia({ id: 7, state: 'failed', error: 'x' });
    const api = fakeApi({
      messages: vi.fn(async () => [makeMessage({ id: 1, media: [failed] }), makeMessage({ id: 2, media: [failed] })]),
    });
    const s = createStore(api);
    await s.refreshLatest(10);
    await s.retryMedia(s.conv(10).items[0], 7);
    expect(s.conv(10).items.map((m) => m.media[0].state)).toEqual(['pending', 'pending']);

    api.retryMedia = vi.fn(async () => Promise.reject(new ApiError(409, 'media is not in failed state', null)));
    await s.retryMedia(s.conv(10).items[0], 7);
    expect(s.toast.value?.text).toBe('media is not in failed state');
    expect(api.message).toHaveBeenCalledWith(1);
  });

  it('resync reloads bots, chats and every open conversation', async () => {
    const api = fakeApi({ messages: vi.fn(async () => page(1, 2)) });
    const s = createStore(api);
    await s.refreshLatest(10);
    await s.resync();
    expect(api.bots).toHaveBeenCalledTimes(1);
    expect(api.chats).toHaveBeenCalledTimes(1);
    expect(api.messages).toHaveBeenCalledTimes(2);
  });

  it('resync tells event listeners once it is done', async () => {
    const s = createStore(fakeApi());
    const seen: string[] = [];
    s.onEvent((ev) => seen.push(ev.type));
    await s.resync();
    expect(seen).toEqual(['resync']);
  });
});

describe('bot timelines', () => {
  // Bot 1 has chats 10 (alice) and 11 (bob); its merged timeline is conversation -1.
  const setup = async (over: Parameters<typeof fakeApi>[0] = {}) => {
    const api = fakeApi({
      bots: vi.fn(async () => [makeBot({ id: 1 })]),
      chats: vi.fn(async () => [makeChat({ id: 10, bot_id: 1 }), makeChat({ id: 11, bot_id: 1 })]),
      botMessages: vi.fn(async () => [makeMessage({ id: 1, chat_id: 10 }), makeMessage({ id: 2, chat_id: 11 })]),
      ...over,
    });
    const s = createStore(api, { chatsReloadDelay: 0 });
    await s.loadBots();
    await s.loadChats();
    return { api, s };
  };

  it('loads a negative key from the bot endpoint', async () => {
    const { api, s } = await setup();
    await s.refreshLatest(-1);
    expect(api.botMessages).toHaveBeenCalledWith(1, 0, 50);
    expect(api.messages).not.toHaveBeenCalled();
    expect(ids(s.conv(-1).items)).toEqual([1, 2]);
  });

  it('writes a new message into both its chat and its bot timeline', async () => {
    const { api, s } = await setup({
      messages: vi.fn(async () => [makeMessage({ id: 1, chat_id: 10 })]),
      message: vi.fn(async (id: number) => makeMessage({ id, chat_id: 10 })),
    });
    await s.refreshLatest(10);
    await s.refreshLatest(-1);
    await s.handleEvent({ type: 'message.created', data: { chat_id: 10, message_id: 3 } });
    expect(ids(s.conv(10).items)).toEqual([1, 3]);
    expect(ids(s.conv(-1).items)).toEqual([1, 2, 3]);
    expect(api.message).toHaveBeenCalledTimes(1);
  });

  it('learns the chat of a new sender before filing it into an open bot timeline', async () => {
    const { api, s } = await setup({ message: vi.fn(async (id: number) => makeMessage({ id, chat_id: 12 })) });
    await s.refreshLatest(-1);
    api.chats = vi.fn(async () => [makeChat({ id: 10, bot_id: 1 }), makeChat({ id: 11, bot_id: 1 }), makeChat({ id: 12, bot_id: 1 })]);
    await s.handleEvent({ type: 'message.created', data: { chat_id: 12, message_id: 5 } });
    expect(ids(s.conv(-1).items)).toEqual([1, 2, 5]);
  });

  it('ignores messages of other bots', async () => {
    const { api, s } = await setup({ message: vi.fn(async (id: number) => makeMessage({ id, chat_id: 20 })) });
    await s.refreshLatest(-1);
    api.chats = vi.fn(async () => [makeChat({ id: 10, bot_id: 1 }), makeChat({ id: 11, bot_id: 1 }), makeChat({ id: 20, bot_id: 2 })]);
    await s.handleEvent({ type: 'message.created', data: { chat_id: 20, message_id: 9 } });
    expect(api.message).not.toHaveBeenCalled();
    expect(ids(s.conv(-1).items)).toEqual([1, 2]);
  });

  it('removes a deleted message from the bot timeline too', async () => {
    const { s } = await setup({ messages: vi.fn(async () => [makeMessage({ id: 2, chat_id: 11 })]) });
    await s.refreshLatest(11);
    await s.refreshLatest(-1);
    await s.handleEvent({ type: 'message.deleted', data: { chat_id: 11, message_id: 2 } });
    expect(ids(s.conv(-1).items)).toEqual([1]);
    expect(ids(s.conv(11).items)).toEqual([]);
  });

  it('marks a retried media pending in every conversation holding it', async () => {
    const msg = makeMessage({ id: 2, chat_id: 11, media: [makeMedia({ id: 7, state: 'failed' })] });
    const { s } = await setup({ messages: vi.fn(async () => [msg]), botMessages: vi.fn(async () => [msg]) });
    await s.refreshLatest(11);
    await s.refreshLatest(-1);
    await s.retryMedia(msg, 7);
    expect(s.conv(11).items[0].media[0].state).toBe('pending');
    expect(s.conv(-1).items[0].media[0].state).toBe('pending');
  });

  it('lists bots by their latest chat and counts senders', async () => {
    const api = fakeApi({
      bots: vi.fn(async () => [makeBot({ id: 1 }), makeBot({ id: 2 }), makeBot({ id: 3, status: 'removed' })]),
      chats: vi.fn(async () => [
        makeChat({ id: 10, bot_id: 1, last_message_at: 5 }),
        makeChat({ id: 11, bot_id: 1, last_message_at: 9 }),
        makeChat({ id: 20, bot_id: 2, last_message_at: 7 }),
      ]),
    });
    const s = createStore(api);
    await s.loadBots();
    await s.loadChats();
    expect(s.botEntries.value.map((e) => [e.bot.id, e.last?.id, e.senders])).toEqual([
      [1, 11, 2],
      [2, 20, 1],
    ]);
  });

  it('persists the list mode and survives unavailable storage', () => {
    localStorage.removeItem('tgarchive.listMode');
    const s = createStore(fakeApi());
    expect(s.listMode.value).toBe('people');
    s.setListMode('bot');
    expect(createStore(fakeApi()).listMode.value).toBe('bot');
    const spy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    const set = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    try {
      const t = createStore(fakeApi());
      expect(t.listMode.value).toBe('people');
      t.setListMode('bot');
      expect(t.listMode.value).toBe('bot');
    } finally {
      spy.mockRestore();
      set.mockRestore();
      localStorage.removeItem('tgarchive.listMode');
    }
  });
});

describe('download progress', () => {
  const snapshot = (over = {}) => ({
    active: [{ media_id: 7, message_id: 1, chat_id: 10, kind: 'video', file_name: '', size: 100, done: 10, total: 100, started_at: 1 }],
    queued: { count: 2, bytes: 300 },
    failed: [{ media_id: 9, message_id: 2, chat_id: 10, kind: 'photo', file_name: '', size: 4, error: 'x' }],
    speed: 5,
    ...over,
  });

  it('tracks progress events and summarises them', async () => {
    const api = fakeApi({ downloads: vi.fn(async () => snapshot()) });
    const s = createStore(api, { downloadsReloadDelay: 0 });
    await s.loadDownloads();
    await s.handleEvent({
      type: 'download.progress',
      data: {
        items: [
          { media_id: 7, done: 50, total: 100, started_at: 1 },
          { media_id: 8, done: 10, total: 0, started_at: 2 },
        ],
        speed: 42,
      },
    });
    expect(s.progress.value.get(7)).toEqual({ done: 50, total: 100 });
    expect(s.downloadSummary.value).toEqual({ active: 2, fraction: 0.5, queued: 2, failed: 1 });
    expect(s.downloads.value?.speed).toBe(42);
  });

  it('refreshes the snapshot, debounced, when the set of downloads changes', async () => {
    vi.useFakeTimers();
    try {
      const api = fakeApi({ downloads: vi.fn(async () => snapshot()) });
      const s = createStore(api, { downloadsReloadDelay: 1000 });
      await s.loadDownloads();
      const tick = (ids: number[]) =>
        s.handleEvent({ type: 'download.progress', data: { items: ids.map((id) => ({ media_id: id, done: 1, total: 2, started_at: 1 })), speed: 0 } });
      await tick([7]); // the first event names a set: one refresh
      await tick([7]);
      await vi.advanceTimersByTimeAsync(1500);
      expect(api.downloads).toHaveBeenCalledTimes(2);
      await tick([7]); // unchanged: none
      await vi.advanceTimersByTimeAsync(1500);
      expect(api.downloads).toHaveBeenCalledTimes(2);
      await tick([7, 8]);
      await tick([7, 8]);
      await vi.advanceTimersByTimeAsync(1500);
      expect(api.downloads).toHaveBeenCalledTimes(3);
      await tick([]); // all done: the final empty event also refreshes
      expect(s.downloadSummary.value.active).toBe(0);
      await vi.advanceTimersByTimeAsync(1500);
      expect(api.downloads).toHaveBeenCalledTimes(4);
    } finally {
      vi.useRealTimers();
    }
  });

  it('takes the live progress from a fresh snapshot, clearing downloads that ended while away', async () => {
    const api = fakeApi({ downloads: vi.fn(async () => snapshot()) });
    const s = createStore(api);
    await s.handleEvent({
      type: 'download.progress',
      data: { items: [{ media_id: 3, done: 1, total: 2, started_at: 1 }], speed: 0 },
    });
    await s.resync(); // the final empty event was missed; the snapshot only has media 7
    expect([...s.progress.value.keys()]).toEqual([7]);
    expect(s.progress.value.get(7)).toEqual({ done: 10, total: 100 });
  });

  it('keeps refreshing while the set of downloads keeps changing (throttled, not starved)', async () => {
    vi.useFakeTimers();
    try {
      const api = fakeApi({ downloads: vi.fn(async () => snapshot({ active: [] })) });
      const s = createStore(api, { downloadsReloadDelay: 1000 });
      for (let i = 1; i <= 5; i++) {
        await s.handleEvent({ type: 'download.progress', data: { items: [{ media_id: i, done: 1, total: 2, started_at: 1 }], speed: 0 } });
        await vi.advanceTimersByTimeAsync(600);
      }
      expect((api.downloads as ReturnType<typeof vi.fn>).mock.calls.length).toBeGreaterThanOrEqual(2);
      // An unchanged set does not refresh at all, even if the snapshot disagrees with it.
      await vi.advanceTimersByTimeAsync(2000);
      const before = (api.downloads as ReturnType<typeof vi.fn>).mock.calls.length;
      for (let i = 0; i < 3; i++) {
        await s.handleEvent({ type: 'download.progress', data: { items: [{ media_id: 99, done: 1, total: 2, started_at: 1 }], speed: 0 } });
        await vi.advanceTimersByTimeAsync(1100);
      }
      expect((api.downloads as ReturnType<typeof vi.fn>).mock.calls.length).toBe(before + 1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('keeps the last snapshot when loading fails', async () => {
    const api = fakeApi({ downloads: vi.fn(async () => snapshot()) });
    const s = createStore(api);
    await s.loadDownloads();
    api.downloads = vi.fn(async () => Promise.reject(new Error('down')));
    await s.loadDownloads();
    expect(s.downloads.value?.queued.count).toBe(2);
  });

  it('retries from the panel and reloads', async () => {
    const api = fakeApi({ downloads: vi.fn(async () => snapshot()) });
    const s = createStore(api);
    await s.retryDownload(9);
    expect(api.retryMedia).toHaveBeenCalledWith(9);
    expect(api.downloads).toHaveBeenCalledTimes(1);
    api.retryMedia = vi.fn(async () => Promise.reject(new ApiError(409, 'media is not in failed state', null)));
    await s.retryDownload(9);
    expect(s.toast.value?.text).toBe('media is not in failed state');
  });

  it('resync reloads the snapshot', async () => {
    const api = fakeApi();
    const s = createStore(api);
    await s.resync();
    expect(api.downloads).toHaveBeenCalledTimes(1);
  });
});

describe('conversation windows', () => {
  // Messages 1..200 exist; the API serves before / after / around like the server.
  const all = page(1, 200);
  const serve = vi.fn(async (_chat: number, before = 0, limit = 50, extra: { after?: number; around?: number } = {}) => {
    if (extra.after) return all.filter((m) => m.id > extra.after!).slice(0, limit);
    if (extra.around) {
      const older = all.filter((m) => m.id <= extra.around!).slice(-(limit - Math.floor(limit / 2)));
      const newer = all.filter((m) => m.id > extra.around!).slice(0, Math.floor(limit / 2));
      return [...older, ...newer];
    }
    const below = all.filter((m) => before === 0 || m.id < before);
    return below.slice(-limit);
  });

  it('loads a window around a message, then newer pages until the latest', async () => {
    const s = createStore(fakeApi({ messages: serve }));
    await s.loadAround(10, 60);
    expect(serve).toHaveBeenLastCalledWith(10, 0, 50, { around: 60 });
    expect(ids(s.conv(10).items)).toEqual(ids(page(36, 85)));
    expect(s.conv(10)).toMatchObject({ hasMore: true, hasNewer: true, loaded: true });
    await s.loadNewer(10);
    expect(serve).toHaveBeenLastCalledWith(10, 0, 50, { after: 85 });
    expect(ids(s.conv(10).items)).toEqual(ids(page(36, 135)));
    await s.loadNewer(10);
    await s.loadNewer(10);
    expect(s.conv(10).items.at(-1)?.id).toBe(200);
    expect(s.conv(10).hasNewer).toBe(false);
    const calls = serve.mock.calls.length;
    await s.loadNewer(10);
    expect(serve.mock.calls.length).toBe(calls);
  });

  it('marks the start of history when the window reaches it', async () => {
    const s = createStore(fakeApi({ messages: serve }));
    await s.loadAround(10, 5);
    expect(s.conv(10)).toMatchObject({ hasMore: false, hasNewer: true });
  });

  it('keeps live messages out of a window that does not reach the latest', async () => {
    const api = fakeApi({ messages: serve, message: vi.fn(async (id: number) => makeMessage({ id, chat_id: 10, text: 'edited' })) });
    const s = createStore(api, { chatsReloadDelay: 0 });
    await s.loadAround(10, 60);
    await s.handleEvent({ type: 'message.created', data: { chat_id: 10, message_id: 201 } });
    expect(s.conv(10).items.at(-1)?.id).toBe(85);
    await s.handleEvent({ type: 'message.updated', data: { chat_id: 10, message_id: 60 } });
    expect(s.conv(10).items.find((m) => m.id === 60)?.text).toBe('edited');
  });

  it('drops a page that arrives after a jump replaced the conversation', async () => {
    let releaseOlder: (ms: Message[]) => void = () => {};
    const messages = vi.fn(async (_c: number, before = 0, limit = 50, extra: { after?: number; around?: number } = {}) => {
      if (before) return new Promise<Message[]>((r) => (releaseOlder = r));
      return serve(_c, before, limit, extra);
    });
    const s = createStore(fakeApi({ messages }));
    await s.refreshLatest(10); // 151..200
    const older = s.loadOlder(10); // in flight
    await s.loadAround(10, 60); // the user jumps meanwhile
    releaseOlder(page(101, 150));
    await older;
    expect(ids(s.conv(10).items)).toEqual(ids(page(36, 85)));
    expect(s.conv(10)).toMatchObject({ hasMore: true, hasNewer: true, loading: false });
  });

  it('keeps only the latest of two jumps', async () => {
    let releaseFirst: (ms: Message[]) => void = () => {};
    const messages = vi.fn(async (_c: number, before = 0, limit = 50, extra: { after?: number; around?: number } = {}) =>
      extra.around === 20 ? new Promise<Message[]>((r) => (releaseFirst = r)) : serve(_c, before, limit, extra),
    );
    const s = createStore(fakeApi({ messages }));
    const first = s.loadAround(10, 20);
    await s.loadAround(10, 60);
    releaseFirst(page(1, 45));
    await first;
    expect(ids(s.conv(10).items)).toEqual(ids(page(36, 85)));
  });

  it('replaces a window with the latest page on refresh', async () => {
    const s = createStore(fakeApi({ messages: serve }));
    await s.loadAround(10, 60);
    await s.refreshLatest(10);
    expect(ids(s.conv(10).items)).toEqual(ids(page(151, 200)));
    expect(s.conv(10).hasNewer).toBe(false);
    await s.refreshLatest(10, { reset: true });
    expect(ids(s.conv(10).items)).toEqual(ids(page(151, 200)));
  });

  it('keeps a window short of the latest on resync', async () => {
    const s = createStore(fakeApi({ messages: serve }));
    await s.loadAround(10, 60);
    await s.resync();
    expect(ids(s.conv(10).items)).toEqual(ids(page(36, 85)));
    expect(s.conv(10)).toMatchObject({ hasMore: true, hasNewer: true, loading: false });
    await s.loadNewer(10);
    expect(ids(s.conv(10).items)).toEqual(ids(page(36, 135)));
  });

  it('extends a window that reaches the latest page on resync', async () => {
    const s = createStore(fakeApi({ messages: serve }));
    await s.loadAround(10, 150); // 126..175, newer posts exist
    await s.resync();
    expect(ids(s.conv(10).items)).toEqual(ids(page(126, 200)));
    expect(s.conv(10).hasNewer).toBe(false);
  });

  it('keeps the list on resync when more arrived than one page, leaving the rest to load', async () => {
    let top = 120;
    const messages = vi.fn(async (_c: number, _before = 0, limit = 50, extra: { after?: number } = {}) => {
      const have = all.filter((m) => m.id <= top);
      return extra.after ? have.filter((m) => m.id > extra.after!).slice(0, limit) : have.slice(-limit);
    });
    const s = createStore(fakeApi({ messages }));
    await s.refreshLatest(10); // 71..120, the latest
    top = 200; // 80 more while disconnected
    await s.resync();
    expect(ids(s.conv(10).items)).toEqual(ids(page(71, 120)));
    expect(s.conv(10).hasNewer).toBe(true);
    await s.loadNewer(10);
    expect(ids(s.conv(10).items)).toEqual(ids(page(71, 170)));
  });
});

describe('read marks', () => {
  const setup = async () => {
    const api = fakeApi({ chats: vi.fn(async () => [makeChannelChat({ id: 50, unread: 4, last_read_pos: 10, first_unread_id: 11 }), makeChat({ id: 10 })]) });
    const s = createStore(api, { chatsReloadDelay: 0 });
    await s.loadChats();
    return { s, api };
  };

  it('reports a channel read up to a message at most once a second, clearing its badge at once', async () => {
    vi.useFakeTimers();
    const { s, api } = await setup();
    s.markRead(50, { id: 20, pos: 20 });
    expect(api.markRead).toHaveBeenCalledWith(50, 20);
    expect(s.chats.value.find((c) => c.id === 50)).toMatchObject({ unread: 0, last_read_pos: 20 });
    s.markRead(50, { id: 21, pos: 21 });
    s.markRead(50, { id: 22, pos: 22 });
    expect(api.markRead).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1000);
    expect(api.markRead).toHaveBeenCalledTimes(2);
    expect(api.markRead).toHaveBeenLastCalledWith(50, 22);
    vi.useRealTimers();
  });

  it('ignores private chats and messages already read', async () => {
    const { s, api } = await setup();
    s.markRead(10, { id: 99, pos: 99 });
    s.markRead(50, { id: 5, pos: 5 });
    expect(api.markRead).not.toHaveBeenCalled();
  });

  it('reloads the chat list when another device read a chat', async () => {
    const { s, api } = await setup();
    await s.handleEvent({ type: 'chat.read', data: { chat_id: 50 } });
    await new Promise((r) => setTimeout(r, 5));
    expect(api.chats).toHaveBeenCalledTimes(2);
  });
});
