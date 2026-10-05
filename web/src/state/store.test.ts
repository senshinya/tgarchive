import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '../api/client';
import type { Message } from '../api/types';
import { fakeApi, makeBot, makeChat, makeMedia, makeMessage } from '../test/fixtures';
import { createStore } from './store';

const page = (from: number, to: number, chat = 10): Message[] =>
  Array.from({ length: to - from + 1 }, (_, i) => makeMessage({ id: from + i, chat_id: chat }));

const ids = (ms: Message[]) => ms.map((m) => m.id);

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
