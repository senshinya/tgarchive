import { act, fireEvent, screen } from '@testing-library/preact';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Api } from '../../api/client';
import { route } from '../../lib/router';
import { SILENT_KEY, setSilent, silent } from '../../lib/silent';
import { fakeApi, makeBot, makeChannelChat, makeChat, makeMessage } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { ChatsPanel } from './ChatsPanel';

afterEach(() => {
  setSilent(false);
  route.value = { name: 'home' };
  history.replaceState(null, '', '/');
});

async function setup(bots = [makeBot({ id: 1, name: 'Alpha' }), makeBot({ id: 2, name: 'Beta', tg_bot_id: 888 })]) {
  const api = fakeApi({
    bots: vi.fn(async () => bots),
    chats: vi.fn(async () => [
      makeChat({ id: 10, bot_id: 1, last_text: 'hello' }),
      makeChat({ id: 11, bot_id: 2, last_kind: 'photo', last_text: '', sender: { tg_user_id: 7, first_name: 'Bob', last_name: 'Lee', username: '', has_avatar: true } }),
    ]),
  });
  const r = renderWithStore(<ChatsPanel />, api);
  await act(async () => {
    await r.store.loadBots();
    await r.store.loadChats();
  });
  return r;
}

describe('ChatsPanel', () => {
  it('switches 静音模式 from the header and remembers it on this device', async () => {
    const r = await setup();
    const btn = screen.getByRole('button', { name: '开启静音模式' });
    expect(btn.getAttribute('aria-pressed')).toBe('false');
    fireEvent.click(btn);
    expect(silent.value).toBe(true);
    expect(localStorage.getItem(SILENT_KEY)).toBe('1');
    const on = screen.getByRole('button', { name: '关闭静音模式' });
    expect(on.getAttribute('aria-pressed')).toBe('true');
    expect(r.store.toast.value?.text).toBe('已开启静音模式：所有视频与语音静音播放');
    fireEvent.click(on);
    expect(silent.value).toBe(false);
    expect(localStorage.getItem(SILENT_KEY)).toBeNull();
  });

  it('lists chats with previews and bot prefixes in the 全部 view', async () => {
    const { container } = await setup();
    const items = container.querySelectorAll('.ChatItem');
    expect(items).toHaveLength(2);
    expect(items[0].textContent).toContain('Alice');
    expect(items[0].textContent).toContain('Alpha: hello');
    expect(items[1].textContent).toContain('Bob Lee');
    expect(items[1].textContent).toContain('Beta: 照片');
    expect(items[1].querySelector('img')!.getAttribute('src')).toBe('/avatars/senders/7');
  });

  it('filters by bot with the tab bar', async () => {
    const { container } = await setup();
    fireEvent.click(screen.getByRole('tab', { name: 'Beta' }));
    const items = container.querySelectorAll('.ChatItem');
    expect(items).toHaveLength(1);
    expect(items[0].textContent).not.toContain('Beta:');
    fireEvent.click(screen.getByRole('tab', { name: '全部' }));
    expect(container.querySelectorAll('.ChatItem')).toHaveLength(2);
  });

  it('hides the tab bar and prefixes with a single bot', async () => {
    const { container } = await setup([makeBot({ id: 1, name: 'Alpha' })]);
    expect(screen.queryByRole('tablist')).toBeNull();
    expect(container.textContent).not.toContain('Alpha:');
  });

  it('opens a chat and marks it selected', async () => {
    const { container } = await setup();
    fireEvent.click(container.querySelectorAll('.ChatItem')[1]);
    expect(route.value).toEqual({ name: 'chat', chatId: 11 });
    expect(location.pathname).toBe('/chat/11');
    expect(container.querySelectorAll('.ChatItem')[1].classList.contains('selected')).toBe(true);
  });

  it('shows all chats again, instead of staying stuck empty, after the filtered bot is purged', async () => {
    const { container, store, api } = await setup();
    fireEvent.click(screen.getByRole('tab', { name: 'Beta' }));
    expect(container.querySelectorAll('.ChatItem')).toHaveLength(1);

    // Bot 2 gets purged; the chats panel reloads (as BotSettings.remove() does).
    api.bots = vi.fn(async () => [makeBot({ id: 1, name: 'Alpha' })]);
    api.chats = vi.fn(async () => [makeChat({ id: 10, bot_id: 1 })]);
    await act(async () => {
      await store.loadBots();
      await store.loadChats();
    });
    expect(container.querySelectorAll('.ChatItem')).toHaveLength(1);
    expect(container.querySelectorAll('.ChatItem')[0].textContent).toContain('Alice');
  });

  it('shows the empty state and opens settings from the gear', async () => {
    const r = renderWithStore(<ChatsPanel />);
    await act(async () => {
      await r.store.loadChats();
    });
    expect(screen.getByText('暂无存档')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: '管理' }));
    expect(route.value).toEqual({ name: 'settings' });
  });
});

describe('ChatsPanel bot mode', () => {
  afterEach(() => localStorage.removeItem('tgarchive.listMode'));

  it('switches to one row per bot, remembers it, and opens the merged timeline', async () => {
    const { container, store } = await setup();
    fireEvent.click(screen.getByRole('button', { name: '按 bot' }));
    expect(store.listMode.value).toBe('bot');
    expect(localStorage.getItem('tgarchive.listMode')).toBe('bot');
    expect(screen.queryByRole('tab', { name: '全部' })).toBeNull();
    const items = container.querySelectorAll('.ChatItem');
    expect(items).toHaveLength(2);
    const alpha = [...items].find((i) => i.textContent!.includes('Alpha'))!;
    expect(alpha.textContent).toContain('Alice: hello');
    fireEvent.click(alpha);
    expect(route.value).toEqual({ name: 'bot', botId: 1 });
    expect(alpha.getAttribute('aria-current')).toBe('page');
    fireEvent.click(screen.getByRole('button', { name: '按人' }));
    expect(container.querySelectorAll('.ChatItem')).toHaveLength(2);
    expect(screen.getByRole('tab', { name: '全部' })).toBeTruthy();
  });

  it('shows the switch with a single bot', async () => {
    await setup([makeBot({ id: 1, name: 'Alpha' })]);
    expect(screen.getByRole('button', { name: '按 bot' }).getAttribute('aria-pressed')).toBe('false');
  });
});

describe('search', () => {
  const hit = (id: number, chat: number, snippet: string, ranges: [number, number][], field: 'body' | 'files' | 'article' = 'body') => ({
    message: makeMessage({ id, chat_id: chat, text: snippet }),
    field,
    snippet,
    ranges,
  });

  async function searchSetup(search = vi.fn<Api['search']>(async () => ({ items: [hit(5, 10, '今天天气很好', [[2, 2]])], next: 0 }))) {
    vi.useFakeTimers();
    const api = fakeApi({
      bots: vi.fn(async () => [makeBot({ id: 1, name: 'Alpha' })]),
      chats: vi.fn(async () => [makeChat({ id: 10, bot_id: 1, last_text: 'hello' })]),
      search,
    });
    const r = renderWithStore(<ChatsPanel />, api);
    await act(async () => {
      await r.store.loadBots();
      await r.store.loadChats();
    });
    return { ...r, search };
  }

  afterEach(() => vi.useRealTimers());

  it('searches 300ms after typing and shows highlighted results in place of the chat list', async () => {
    const r = await searchSetup();
    const box = screen.getByRole('searchbox', { name: '搜索消息' });
    fireEvent.input(box, { target: { value: '天' } });
    fireEvent.input(box, { target: { value: '天气' } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(299);
    });
    expect(r.search).not.toHaveBeenCalled();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(r.search).toHaveBeenCalledTimes(1);
    expect(r.search).toHaveBeenCalledWith('天气', 0, 0);
    expect(r.container.querySelector('.SearchResult mark')?.textContent).toBe('天气');
    expect(r.container.querySelector('.ChatItem:not(.SearchResult)')).toBeNull();
  });

  it('opens the conversation at a result and keeps the results for coming back', async () => {
    const r = await searchSetup();
    fireEvent.input(screen.getByRole('searchbox', { name: '搜索消息' }), {
      target: { value: '天气' },
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    fireEvent.click(r.container.querySelector('.SearchResult')!);
    expect(r.store.jumpTo.value).toEqual({ key: 10, messageId: 5 });
    expect(route.value).toEqual({ name: 'chat', chatId: 10 });
    expect(r.store.searchQuery.value).toBe('天气');
  });

  it('clears on Escape and shows the chat list again', async () => {
    const r = await searchSetup();
    const box = screen.getByRole('searchbox', { name: '搜索消息' });
    fireEvent.input(box, { target: { value: '天气' } });
    fireEvent.keyDown(box, { key: 'Escape' });
    expect(r.store.searchQuery.value).toBe('');
    expect(r.container.querySelector('.ChatItem')).not.toBeNull();
  });

  it('says so when nothing matches', async () => {
    await searchSetup(vi.fn(async () => ({ items: [], next: 0 })));
    fireEvent.input(screen.getByRole('searchbox', { name: '搜索消息' }), {
      target: { value: 'zzz' },
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    expect(screen.getByText('没有找到相关消息')).toBeTruthy();
  });

  it('limits the search to one conversation behind a removable chip', async () => {
    const r = await searchSetup();
    act(() => r.store.openSearch(10));
    expect(screen.getByRole('button', { name: /取消限定/ }).textContent).toContain('Alice');
    fireEvent.input(screen.getByRole('searchbox', { name: '搜索消息' }), {
      target: { value: '天气' },
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    expect(r.search).toHaveBeenLastCalledWith('天气', 10, 0);
    fireEvent.click(screen.getByRole('button', { name: /取消限定/ }));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    expect(r.search).toHaveBeenLastCalledWith('天气', 0, 0);
  });

  it('focuses the box without scrolling the layout (the column may still be sliding in)', async () => {
    const r = await searchSetup();
    const focus = vi.spyOn(HTMLElement.prototype, 'focus');
    act(() => r.store.openSearch(10));
    expect(focus).toHaveBeenCalledWith({ preventScroll: true });
    focus.mockRestore();
  });

  it('loads the next page when scrolled to the end', async () => {
    const search = vi.fn(async (_q: string, _c?: number, before?: number) =>
      before ? { items: [hit(3, 10, '天气 b', [[0, 2]])], next: 0 } : { items: [hit(5, 10, '天气 a', [[0, 2]])], next: 5 },
    );
    const r = await searchSetup(search);
    fireEvent.input(screen.getByRole('searchbox', { name: '搜索消息' }), {
      target: { value: '天气' },
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    await act(async () => {
      fireEvent.scroll(r.container.querySelector('.SearchResults')!);
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(search).toHaveBeenLastCalledWith('天气', 0, 5);
    expect(r.container.querySelectorAll('.SearchResult').length).toBe(2);
  });
});

describe('unread badges', () => {
  async function badgeSetup(unread: number, selected = false) {
    const api = fakeApi({ chats: vi.fn(async () => [makeChannelChat({ id: 50, unread }), makeChat({ id: 10, unread: 0 })]) });
    if (selected) route.value = { name: 'chat', chatId: 50 };
    const r = renderWithStore(<ChatsPanel />, api);
    await act(async () => {
      await r.store.loadChats();
    });
    return r;
  }

  it('counts a channel’s new posts', async () => {
    const r = await badgeSetup(3);
    expect(r.container.querySelector('.ChannelItem .ChatBadge')?.textContent).toBe('3');
    expect(r.container.querySelectorAll('.ChatBadge').length).toBe(1);
  });

  it('caps the number', async () => {
    const r = await badgeSetup(1000);
    expect(r.container.querySelector('.ChatBadge')?.textContent).toBe('999+');
  });

  it('hides it on the open conversation and when nothing is new', async () => {
    expect((await badgeSetup(3, true)).container.querySelector('.ChatBadge')).toBeNull();
    route.value = { name: 'home' };
    expect((await badgeSetup(0)).container.querySelectorAll('.ChatBadge').length).toBe(0);
  });
});
