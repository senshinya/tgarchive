import { act, fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { navigate, route } from '../../lib/router';
import { fakeApi, makeBot, makeChannelChat, makeChat, makeMessage } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { createStore, StoreContext } from '../../state/store';
import { MiddleColumn } from './MiddleColumn';

afterEach(() => {
  route.value = { name: 'home' };
  history.replaceState(null, '', '/');
});

async function setup() {
  const api = fakeApi({ chats: vi.fn(async () => [makeChat({ id: 10 })]) });
  const r = renderWithStore(<MiddleColumn chatId={10} />, api);
  await act(async () => {
    await r.store.loadChats();
  });
  return r;
}

describe('MiddleColumn back button', () => {
  it('goes back in history (not a fresh "/" push) when the chat was opened from the chat list', async () => {
    await setup();
    // Simulates how ChatsPanel opens a chat: navigate(..., { fromList: true }).
    navigate({ name: 'chat', chatId: 10 }, { fromList: true });
    const back = vi.spyOn(history, 'back').mockImplementation(() => undefined);
    fireEvent.click(screen.getByRole('button', { name: '返回' }));
    expect(back).toHaveBeenCalledTimes(1);
    back.mockRestore();
  });

  it('navigates to home (does not call history.back()) when the chat was not opened from the list, e.g. a deep link', async () => {
    await setup();
    history.replaceState({ fromList: false }, '', '/chat/10');
    const back = vi.spyOn(history, 'back').mockImplementation(() => undefined);
    fireEvent.click(screen.getByRole('button', { name: '返回' }));
    expect(back).not.toHaveBeenCalled();
    expect(route.value).toEqual({ name: 'home' });
    expect(location.pathname).toBe('/');
    back.mockRestore();
  });

  it('navigates to home when there is no history state at all (e.g. a fresh deep link with no prior navigate() call)', async () => {
    await setup();
    history.replaceState(null, '', '/chat/10');
    const back = vi.spyOn(history, 'back').mockImplementation(() => undefined);
    fireEvent.click(screen.getByRole('button', { name: '返回' }));
    expect(back).not.toHaveBeenCalled();
    expect(route.value).toEqual({ name: 'home' });
    back.mockRestore();
  });
});

describe('MiddleColumn bot timeline', () => {
  it('shows the bot and its sender count, and labels each sender group', async () => {
    const api = fakeApi({
      bots: vi.fn(async () => [makeBot({ id: 1, name: 'Alpha' })]),
      chats: vi.fn(async () => [
        makeChat({ id: 10, bot_id: 1 }),
        makeChat({ id: 11, bot_id: 1, sender: { tg_user_id: 7, first_name: 'Bob', last_name: '', username: '', has_avatar: false } }),
      ]),
      botMessages: vi.fn(async () => [
        makeMessage({ id: 1, chat_id: 10, text: 'from alice' }),
        makeMessage({ id: 2, chat_id: 11, text: 'from bob' }),
        makeMessage({ id: 3, chat_id: 11, text: 'bob again' }),
      ]),
    });
    const r = renderWithStore(<MiddleColumn chatId={-1} />, api);
    await act(async () => {
      await r.store.loadBots();
      await r.store.loadChats();
    });
    await screen.findByText('from bob');
    expect(api.botMessages).toHaveBeenCalledWith(1, 0, 50);
    expect(screen.getByText('2 位发送人')).toBeTruthy();
    const names = [...r.container.querySelectorAll('.sender-title')].map((e) => e.textContent);
    expect(names).toEqual(['Alice', 'Bob']);
    expect(r.container.querySelectorAll('.message-group-avatar')).toHaveLength(2);
  });

  it('does not label senders in a single chat', async () => {
    const api = fakeApi({
      chats: vi.fn(async () => [makeChat({ id: 10 })]),
      messages: vi.fn(async () => [makeMessage({ id: 1, chat_id: 10, text: 'hi' })]),
    });
    const r = renderWithStore(<MiddleColumn chatId={10} />, api);
    await screen.findByText('hi');
    expect(r.container.querySelector('.sender-title')).toBeNull();
    expect(r.container.querySelector('.message-group-avatar')).toBeNull();
  });
});

describe('jump from the downloads panel', () => {
  // jsdom lays nothing out: give the list a tall body so it stops filling itself with history.
  beforeEach(() => {
    vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(5000);
    vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(500);
  });
  afterEach(() => vi.restoreAllMocks());

  const pageOf = (from: number, to: number) =>
    Array.from({ length: to - from + 1 }, (_, i) => makeMessage({ id: from + i, chat_id: 10, text: `m${from + i}` }));

  // Messages 1..100, served like the API does.
  const serve = vi.fn(async (_chat: number, before = 0, limit = 50, page: { around?: number; after?: number } = {}) => {
    const all = pageOf(1, 100);
    if (page.around) {
      const half = Math.floor(limit / 2);
      return [...all.filter((m) => m.id <= page.around!).slice(-(limit - half)), ...all.filter((m) => m.id > page.around!).slice(0, half)];
    }
    if (page.after) return all.filter((m) => m.id > page.after!).slice(0, limit);
    return all.filter((m) => !before || m.id < before).slice(-limit);
  });

  it('loads a window around a message that is not loaded, then clears the request', async () => {
    const api = fakeApi({ chats: vi.fn(async () => [makeChat({ id: 10 })]), messages: serve });
    const r = renderWithStore(<MiddleColumn chatId={10} />, api);
    await screen.findByText('m100');
    r.store.jumpTo.value = { key: 10, messageId: 5 };
    await screen.findByText('m5');
    expect(api.messages).toHaveBeenCalledWith(10, 0, 50, { around: 5 });
    await waitFor(() => expect(r.store.jumpTo.value).toBeNull());
    expect(r.container.querySelector('.Message.highlight')?.textContent).toContain('m5');
    expect(screen.queryByText('m100')).toBeNull();
  });

  it('opens straight on the window when the jump is asked before the conversation loads', async () => {
    serve.mockClear();
    const api = fakeApi({ chats: vi.fn(async () => [makeChat({ id: 10 })]), messages: serve });
    const store = createStore(api, { chatsReloadDelay: 0 });
    store.jumpTo.value = { key: 10, messageId: 5 };
    render(
      <StoreContext.Provider value={store}>
        <MiddleColumn chatId={10} />
      </StoreContext.Provider>,
    );
    await screen.findByText('m5');
    expect(serve.mock.calls[0]).toEqual([10, 0, 50, { around: 5 }]);
  });

  it('goes back to the latest messages from a window', async () => {
    const api = fakeApi({ chats: vi.fn(async () => [makeChat({ id: 10 })]), messages: serve });
    const r = renderWithStore(<MiddleColumn chatId={10} />, api);
    await screen.findByText('m100');
    r.store.jumpTo.value = { key: 10, messageId: 5 };
    await screen.findByText('m5');
    await waitFor(() => expect(r.store.conv(10).hasNewer).toBe(true));
    fireEvent.click(screen.getByRole('button', { name: '回到底部' }));
    await screen.findByText('m100');
    expect(r.store.conv(10).hasNewer).toBe(false);
  });

  it('gives up on a message the conversation does not have', async () => {
    const api = fakeApi({
      chats: vi.fn(async () => [makeChat({ id: 10 })]),
      messages: vi.fn(async () => pageOf(1, 3)),
    });
    const r = renderWithStore(<MiddleColumn chatId={10} />, api);
    r.store.jumpTo.value = { key: 10, messageId: 999 };
    await screen.findByText('m3');
    await waitFor(() => expect(r.store.jumpTo.value).toBeNull());
  });

  it('ignores a request meant for another conversation', async () => {
    const api = fakeApi({ chats: vi.fn(async () => [makeChat({ id: 10 })]), messages: vi.fn(async () => pageOf(1, 3)) });
    const r = renderWithStore(<MiddleColumn chatId={10} />, api);
    r.store.jumpTo.value = { key: -1, messageId: 2 };
    await screen.findByText('m3');
    expect(r.store.jumpTo.value).toEqual({ key: -1, messageId: 2 });
  });
});

describe('MiddleColumn search button', () => {
  it('starts a search limited to this conversation', async () => {
    const r = await setup();
    fireEvent.click(screen.getByRole('button', { name: '搜索此会话' }));
    expect(r.store.searchScope.value).toBe(10);
    expect(r.store.searchFocus.value).toBe(1);
  });

  it('goes back to the chat list on narrow screens, where the list is hidden behind the chat', async () => {
    window.matchMedia = vi.fn(() => ({ matches: true }) as MediaQueryList); // jsdom has none
    await setup();
    navigate({ name: 'chat', chatId: 10 });
    fireEvent.click(screen.getByRole('button', { name: '搜索此会话' }));
    expect(route.value).toEqual({ name: 'home' });
    delete (window as { matchMedia?: unknown }).matchMedia;
  });
});

describe('read marks', () => {
  it('marks a channel read up to its newest message once the bottom is in view', async () => {
    const api = fakeApi({
      chats: vi.fn(async () => [makeChannelChat({ id: 50, unread: 2, last_read_pos: 1, first_unread_id: 2 })]),
      messages: vi.fn(async () => [makeMessage({ id: 2, chat_id: 50, text: 'a' }), makeMessage({ id: 3, chat_id: 50, text: 'b' })]),
    });
    const r = renderWithStore(<MiddleColumn chatId={50} />, api);
    await act(async () => {
      await r.store.loadChats();
    });
    await screen.findByText('b');
    await waitFor(() => expect(api.markRead).toHaveBeenCalledWith(50, 3));
  });

  it('does not for bot chats', async () => {
    const api = fakeApi({ chats: vi.fn(async () => [makeChat({ id: 10 })]), messages: vi.fn(async () => [makeMessage({ id: 3, text: 'b' })]) });
    const r = renderWithStore(<MiddleColumn chatId={10} />, api);
    await act(async () => {
      await r.store.loadChats();
    });
    await screen.findByText('b');
    expect(api.markRead).not.toHaveBeenCalled();
  });
});
