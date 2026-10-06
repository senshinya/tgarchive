import { act, fireEvent, screen, waitFor } from '@testing-library/preact';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/client';
import type { CondGroup, Watch } from '../../api/types';
import { route } from '../../lib/router';
import { formatCount, validateCond } from '../../lib/watchCond';
import { fakeApi, makeChannelChat, makeChannelInfo, makeMessage, makeWatch } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { ChatsPanel } from '../left/ChatsPanel';
import { MessageBubble } from '../message/MessageBubble';
import { MiddleColumn } from '../middle/MiddleColumn';
import { SettingsHome } from '../settings/SettingsHome';
import { CondEditor } from './CondEditor';
import { WatchEditor } from './WatchEditor';
import { WatchList } from './WatchList';

afterEach(() => {
  route.value = { name: 'home' };
  history.replaceState(null, '', '/');
  vi.useRealTimers();
});

const testResult = {
  posts: [
    {
      tg_message_id: 9,
      date: 1_790_000_000,
      kind: 'photo',
      text: 'Big news',
      stats: { reactions: [{ key: '🔥', emoji: '🔥', count: 23 }], total: 23, views: 8100, forwards: 1, replies: 0 },
      hit: true,
      reasons: ['🔥 23 ≥ 10'],
    },
    {
      tg_message_id: 8,
      date: 1_790_000_000,
      kind: 'text',
      text: 'Small news',
      stats: { reactions: [], total: 0, views: 10, forwards: 0, replies: 0 },
      hit: false,
      reasons: [],
    },
  ],
  reactions_available: { all: false, list: [{ key: '🔥', emoji: '🔥', count: 0 }, { key: '👍', emoji: '👍', count: 0 }] },
};

describe('watchCond', () => {
  it('validates like the server and marks the offending nodes', () => {
    const ok: CondGroup = { op: 'and', items: [{ metric: 'views', cmp: 'gte', value: 10 }] };
    expect(validateCond(ok)).toBeNull();
    const bad = new Set<string>();
    const tree: CondGroup = {
      op: 'or',
      items: [
        { metric: 'views', cmp: 'gte', value: 1.5 },
        { op: 'and', items: [] },
        { metric: 'ratio', num: 'total', den: 'total', cmp: 'gte', value: 10 },
        { metric: 'text', cmp: 'contains', value: '  ' },
      ],
    };
    expect(validateCond(tree, bad)).toBe('数量须为非负整数');
    expect([...bad].sort()).toEqual(['0', '1', '2', '3']);
    const deep: CondGroup = { op: 'and', items: [{ op: 'and', items: [{ op: 'and', items: [{ op: 'and', items: [{ metric: 'views', cmp: 'gte', value: 1 }] }] }] }] };
    expect(validateCond(deep)).toBe('条件组最多嵌套 3 层');
    expect(validateCond({ op: 'and', items: [] })).toBe('条件组不能为空');
  });

  it('formats counters', () => {
    expect([999, 1250, 12500, 1_500_000].map(formatCount)).toEqual(['999', '1.2K', '12K', '1.5M']);
  });
});

describe('CondEditor', () => {
  const reactions = testResult.reactions_available;

  it('edits, adds and removes conditions and groups', () => {
    let value: CondGroup = { op: 'and', items: [{ metric: 'reaction', key: '🔥', cmp: 'gte', value: 10 }] };
    const onChange = vi.fn((g: CondGroup) => {
      value = g;
    });
    const { rerender } = renderWithStore(<CondEditor value={value} onChange={onChange} reactions={reactions} bad={new Set()} />);
    const re = () => rerender(<CondEditor value={value} onChange={onChange} reactions={reactions} bad={new Set()} />);

    fireEvent.click(screen.getByRole('button', { name: '任一满足' }));
    re();
    expect(value.op).toBe('or');
    fireEvent.input(screen.getByLabelText('数量'), { target: { value: '25' } });
    re();
    expect(value.items[0]).toMatchObject({ value: 25 });

    fireEvent.click(screen.getByRole('button', { name: '选择表情' }));
    fireEvent.click(screen.getByTitle('👍'));
    re();
    expect(value.items[0]).toMatchObject({ key: '👍' });

    fireEvent.change(screen.getByLabelText('指标'), { target: { value: 'views' } });
    re();
    expect(value.items[0]).toEqual({ metric: 'views', cmp: 'gte', value: 1000 });

    fireEvent.click(screen.getByRole('button', { name: '条件组' }));
    re();
    expect(value.items[1]).toMatchObject({ op: 'and', items: [{ metric: 'reaction', key: '🔥' }] });
    // A third level has no "+ 条件组" of its own.
    // DOM order: the nested group's actions come before the root's.
    const groupButtons = screen.getAllByRole('button', { name: '条件组' });
    fireEvent.click(groupButtons[0]);
    re();
    expect(screen.getAllByRole('button', { name: '条件组' })).toHaveLength(2);

    fireEvent.click(screen.getAllByRole('button', { name: '删除条件组' })[0]);
    re();
    expect(value.items).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: '删除条件' }));
    re();
    expect(value.items).toHaveLength(0);
    expect(screen.getByText('条件组不能为空')).toBeTruthy();
  });

  it('accepts a typed emoji in channels that allow any reaction', () => {
    const onChange = vi.fn();
    renderWithStore(
      <CondEditor value={{ op: 'and', items: [{ metric: 'reaction', key: '🔥', cmp: 'gte', value: 1 }] }} onChange={onChange} reactions={{ all: true, list: [] }} bad={new Set()} />,
    );
    fireEvent.click(screen.getByRole('button', { name: '选择表情' }));
    fireEvent.input(screen.getByLabelText('其他表情'), { target: { value: '❤️' } });
    fireEvent.click(screen.getByRole('button', { name: '确定' }));
    expect(onChange).toHaveBeenLastCalledWith({ op: 'and', items: [{ metric: 'reaction', key: '❤', cmp: 'gte', value: 1 }] });
  });
});

describe('channel conversations in the UI', () => {
  it('shows channel rows, the 频道 tab and the watch button', async () => {
    const api = fakeApi({
      bots: vi.fn(async () => [{ ...makeWatchBot() }]),
      chats: vi.fn(async () => [
        makeChannelChat({ id: 50, last_text: 'post', last_message_at: 2_000_000_000 }),
        makeChannelChat({ id: 51, channel: { channel_id: 501, title: 'Broken', username: '', has_avatar: true }, watch: { id: 4, enabled: true, status: 'error', error: '无法访问', window_minutes: 5, pending: 0, hits: 0 }, last_kind: '' }),
      ]),
    });
    const r = renderWithStore(<ChatsPanel />, api);
    await act(async () => {
      await r.store.loadBots();
      await r.store.loadChats();
    });
    const items = r.container.querySelectorAll('.ChannelItem');
    expect(items).toHaveLength(2);
    expect(items[0].textContent).toContain('News');
    expect(items[1].textContent).toContain('暂无存档');
    expect(items[1].querySelector('.ChatItem-error')).toBeTruthy();
    expect(items[1].querySelector('img')!.getAttribute('src')).toBe('/avatars/channels/501');
    fireEvent.click(screen.getByRole('tab', { name: '频道' }));
    expect(r.container.querySelectorAll('.ChatItem')).toHaveLength(2);
    fireEvent.click(screen.getByRole('button', { name: '按 bot' }));
    expect(r.container.querySelectorAll('.ChannelItem')).toHaveLength(2);
    fireEvent.click(screen.getByRole('button', { name: '监听频道' }));
    expect(route.value).toEqual({ name: 'settings-watch-new' });
    r.store.setListMode('people');
  });

  it('heads a channel conversation with its watch status and settings', async () => {
    const api = fakeApi({ chats: vi.fn(async () => [makeChannelChat()]) });
    const r = renderWithStore(<MiddleColumn chatId={50} />, api);
    await act(async () => {
      await r.store.loadChats();
    });
    expect(screen.getByText('监听中 · 窗口 30 分钟 · 观察中 2 条')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: '监听设置' }));
    expect(route.value).toEqual({ name: 'settings-watch', watchId: 3 });
  });

  it('renders a channel post with reactions, views, signature and the hit detail', () => {
    const msg = makeMessage({
      chat_id: 50,
      source: 'channel_watch',
      text: 'Hello',
      extra: { post_author: 'Editor' },
      stats: {
        reactions: [
          { key: '🔥', emoji: '🔥', count: 23 },
          { key: 'custom:5', custom_id: '5', media_id: 77, mime: 'image/webp', count: 2 },
          { key: 'paid', emoji: '⭐', count: 1200 },
        ],
        total: 1225,
        views: 8123,
        forwards: 3,
        replies: 1,
        hit: { at: 1_790_000_000, reasons: ['🔥 23 ≥ 10'] },
      },
    });
    const { container } = renderWithStore(
      <MessageBubble bubble={{ kind: 'message', key: '1', msg, first: true, last: true }} sender={{ name: 'News', peerId: 500 }} convKey={50} onMenu={() => {}} />,
    );
    expect(container.querySelectorAll('.PostReaction')).toHaveLength(3);
    expect(container.textContent).toContain('1.2K');
    expect(container.querySelector('.PostReaction img')!.getAttribute('src')).toBe('/media/77');
    expect(container.querySelector('.message-views')!.textContent).toBe('8.1K');
    expect(container.querySelector('.message-signature')!.textContent).toBe('Editor');
    expect(container.textContent).not.toContain('🔥 23 ≥ 10');
    fireEvent.click(screen.getByTitle('为何存档'));
    expect(container.textContent).toContain('🔥 23 ≥ 10');
    expect(container.querySelector('.OriginHeader, .origin-title')).toBeNull();
  });
});

function makeWatchBot() {
  return { id: 1, tg_bot_id: 777, username: 'b', name: 'Bot', has_avatar: false, enabled: true, status: 'running', last_error: '' };
}

describe('ChannelPicker (WatchEditor without a watch)', () => {
  it('filters joined channels, searches public ones when nothing matches, and resolves links', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const api = fakeApi({
      channels: vi.fn(async () => [makeChannelInfo(), makeChannelInfo({ channel_id: 501, title: 'Tech', username: 'tech', participants: 0 })]),
      searchChannels: vi.fn(async () => [makeChannelInfo({ channel_id: 900, title: 'Remote', username: 'remote' })]),
      resolveChannel: vi.fn(async () => makeChannelInfo({ channel_id: 777, title: 'Linked' })),
      testWatch: vi.fn(async () => testResult),
    });
    renderWithStore(<WatchEditor />, api);
    expect(await screen.findByText('News')).toBeTruthy();
    expect(screen.getByText('@news · 1.2K 订阅')).toBeTruthy();
    const search = screen.getByLabelText('搜索频道，或粘贴 @用户名 / t.me 链接');
    fireEvent.input(search, { target: { value: 'tec' } });
    expect(screen.queryByText('News')).toBeNull();
    expect(screen.getByText('Tech')).toBeTruthy();
    expect(api.searchChannels).not.toHaveBeenCalled();

    fireEvent.input(search, { target: { value: 'remo' } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });
    expect(api.searchChannels).toHaveBeenCalledWith('remo');
    expect(await screen.findByText('Remote')).toBeTruthy();

    fireEvent.input(search, { target: { value: 't.me/+abc' } });
    expect(screen.getByText(/邀请链接无法直接监听/)).toBeTruthy();
    fireEvent.input(search, { target: { value: '@linked' } });
    await act(async () => {
      fireEvent.click(screen.getByText('解析 @linked'));
    });
    expect(api.resolveChannel).toHaveBeenCalledWith('@linked');
    expect(await screen.findByText('Linked')).toBeTruthy();
    expect(screen.getByRole('button', { name: '开始监听' })).toBeTruthy();
  });

  it('explains when the user account is not logged in', async () => {
    renderWithStore(<WatchEditor />, fakeApi({ channels: vi.fn(async () => Promise.reject(new ApiError(409, '代取账号未登录', null))) }));
    expect(await screen.findByText(/代取账号未登录，无法读取频道/)).toBeTruthy();
  });

  it('sends an already watched channel to its watch', async () => {
    const api = fakeApi({
      channels: vi.fn(async () => [makeChannelInfo({ watched: true })]),
      watches: vi.fn(async () => [makeWatch({ id: 8 })]),
    });
    renderWithStore(<WatchEditor />, api);
    fireEvent.click(await screen.findByText('News'));
    await waitFor(() => expect(route.value).toEqual({ name: 'settings-watch', watchId: 8 }));
  });
});

describe('WatchEditor', () => {
  it('creates a watch from the first allowed reaction and opens its conversation', async () => {
    const api = fakeApi({
      channels: vi.fn(async () => [makeChannelInfo()]),
      testWatch: vi.fn(async () => testResult),
      createWatch: vi.fn(async () => makeWatch({ chat_id: 61 })),
    });
    renderWithStore(<WatchEditor />, api);
    fireEvent.click(await screen.findByText('News'));
    expect(await screen.findByText('Big news')).toBeTruthy();
    expect(screen.getByText('🔥 23 ≥ 10')).toBeTruthy();
    fireEvent.input(screen.getByLabelText('观察窗口（分钟）'), { target: { value: '45' } });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: '开始监听' }));
    });
    expect(api.createWatch).toHaveBeenCalledWith({
      channel_id: 500,
      window_minutes: 45,
      cond: { op: 'and', items: [{ metric: 'reaction', key: '🔥', cmp: 'gte', value: 10 }] },
      enabled: true,
    });
    await waitFor(() => expect(route.value).toEqual({ name: 'chat', chatId: 61 }));
  });

  it('refuses to save an invalid window or condition', async () => {
    const api = fakeApi({ testWatch: vi.fn(async () => testResult), watch: vi.fn(async () => makeWatch({ cond: { op: 'and', items: [] } })) });
    renderWithStore(<WatchEditor watchId={3} />, api);
    fireEvent.input(await screen.findByLabelText('观察窗口（分钟）'), { target: { value: '0' } });
    fireEvent.click(screen.getByRole('button', { name: '保存' }));
    expect(api.updateWatch).not.toHaveBeenCalled();
    expect(screen.getAllByText('观察窗口须为 1–1440 分钟').length).toBeGreaterThan(0);
    expect(screen.getAllByText('条件组不能为空').length).toBeGreaterThan(0);
  });

  it('edits, then deletes keeping or purging the archive', async () => {
    const api = fakeApi({ testWatch: vi.fn(async () => testResult) });
    renderWithStore(<WatchEditor watchId={3} />, api);
    const save = await screen.findByRole('button', { name: '保存' });
    await act(async () => {
      fireEvent.click(save);
    });
    expect(api.updateWatch).toHaveBeenCalledWith(3, expect.objectContaining({ window_minutes: 30, enabled: true }));
    fireEvent.click(screen.getByText('删除监听'));
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: '同时删除已存档的帖子' }));
    });
    expect(api.deleteWatch).toHaveBeenCalledWith(3, true);
    await waitFor(() => expect(route.value).toEqual({ name: 'settings-watches' }));
  });
});

describe('WatchList and settings entry', () => {
  it('lists watches with status, toggles one and saves the poll interval', async () => {
    const list: Watch[] = [makeWatch(), makeWatch({ id: 4, channel: { channel_id: 501, title: 'Broken', username: '', has_avatar: false }, status: 'error', error: '无法访问' })];
    const api = fakeApi({ watches: vi.fn(async () => list), userbot: vi.fn(async () => ({ state: 'ready' as const, phone: '', name: 'Me', tg_user_id: 1, error: '' })) });
    renderWithStore(<WatchList />, api);
    expect(await screen.findByText('观察中 2 · 已存 5 · 窗口 30 分钟')).toBeTruthy();
    expect(screen.getByText('出错：无法访问')).toBeTruthy();
    await act(async () => {
      fireEvent.click(screen.getByRole('switch', { name: '启用 News' }));
    });
    expect(api.updateWatch).toHaveBeenCalledWith(3, expect.objectContaining({ enabled: false }));
    const poll = await screen.findByLabelText('轮询间隔（秒，30–600）');
    await waitFor(() => expect((poll as HTMLInputElement).value).toBe('60'));
    fireEvent.input(poll, { target: { value: '10' } });
    fireEvent.click(screen.getByRole('button', { name: '保存' }));
    expect(screen.getByText('轮询间隔须为 30–600 秒')).toBeTruthy();
    fireEvent.input(screen.getByLabelText('轮询间隔须为 30–600 秒'), { target: { value: '120' } });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: '保存' }));
    });
    expect(api.saveWatchSettings).toHaveBeenCalledWith(120);
  });

  it('warns that watching is paused while the user account is logged out', async () => {
    renderWithStore(<WatchList />);
    expect(await screen.findByText(/监听已暂停/)).toBeTruthy();
  });

  it('shows the entry on the settings home with its count', async () => {
    renderWithStore(<SettingsHome />, fakeApi({ watches: vi.fn(async () => [makeWatch(), makeWatch({ id: 4, status: 'error' })]) }));
    expect(await screen.findByText('2 个监听 · 1 个出错')).toBeTruthy();
    fireEvent.click(screen.getByText('频道监听'));
    expect(route.value).toEqual({ name: 'settings-watches' });
  });
});
