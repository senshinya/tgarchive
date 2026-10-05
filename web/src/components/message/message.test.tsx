import { act, fireEvent, screen, waitFor } from '@testing-library/preact';
import { describe, expect, it, vi } from 'vitest';
import type { Message } from '../../api/types';
import type { Bubble } from '../../lib/grouping';
import { fakeApi, makeBot, makeChat, makeMedia, makeMessage } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { MiddleColumn } from '../middle/MiddleColumn';
import { MessageBubble } from './MessageBubble';

const sender = { name: 'Alice', peerId: 42 };
const single = (msg: Message, first = true, last = true): Bubble => ({ kind: 'message', key: String(msg.id), msg, first, last });

function bubble(b: Bubble, onMenu = vi.fn()) {
  return renderWithStore(<MessageBubble bubble={b} sender={sender} convKey={b.kind === "album" ? b.msgs[0].chat_id : b.msg.chat_id} onMenu={onMenu} />);
}

describe('MessageBubble', () => {
  it('renders text with time, edited mark and the tail on the last bubble', () => {
    const date = new Date(2026, 9, 4, 9, 5).getTime() / 1000;
    const { container } = bubble(single(makeMessage({ text: 'hi there', date, edit_date: date + 60 })));
    expect(container.querySelector('.text-content')!.textContent).toContain('hi there');
    expect(screen.getByText('09:05')).toBeTruthy();
    expect(screen.getByText('已编辑')).toBeTruthy();
    expect(container.querySelector('.message-content')!.className).toContain('has-appendix');
    expect(container.querySelector('.svg-appendix')).toBeTruthy();
  });

  it('marks group position and omits the tail on non-last bubbles', () => {
    const { container } = bubble(single(makeMessage(), false, false));
    const root = container.querySelector('.Message')!;
    expect(root.classList.contains('first-in-group')).toBe(false);
    expect(root.classList.contains('last-in-group')).toBe(false);
    expect(container.querySelector('.svg-appendix')).toBeNull();
  });

  it('shows the forward header with a channel post link', () => {
    bubble(
      single(
        makeMessage({ forward_origin: { type: 'channel', name: 'News', username: 'news', message_id: 9, chat_id: -1001, date: 1 } }),
      ),
    );
    expect(screen.getByText('转发自')).toBeTruthy();
    expect(screen.getByText('News').closest('a')!.getAttribute('href')).toBe('https://t.me/news/9');
  });

  it('shows the protected-origin header for userbot fetches', () => {
    bubble(
      single(
        makeMessage({
          source: 'userbot_fetch',
          origin_chat_title: '内部群',
          origin_link: 'https://t.me/c/123/45',
          extra: { author: 'Bob' },
        }),
      ),
    );
    expect(screen.getByText('内部群').closest('a')!.getAttribute('href')).toBe('https://t.me/c/123/45');
    expect(screen.getByText('· 受保护')).toBeTruthy();
    expect(screen.getByText('· Bob')).toBeTruthy();
  });

  it('renders reply quotes, including replies whose original is not archived', () => {
    bubble(single(makeMessage({ id: 2, reply_to_tg_message_id: 1, reply: { id: 1, kind: 'photo', text: '' } })));
    expect(screen.getByText('Alice')).toBeTruthy();
    expect(screen.getByText('照片')).toBeTruthy();
    bubble(single(makeMessage({ id: 3, reply_to_tg_message_id: 7 })));
    expect(screen.getByText('原消息未存档')).toBeTruthy();
  });

  it('renders unsupported messages as an italic note', () => {
    bubble(single(makeMessage({ kind: 'other', text: '' })));
    expect(screen.getByText('不支持的消息类型')).toBeTruthy();
  });

  it('renders captionless photos edge to edge with the time over the image and opens the viewer', () => {
    const { container, store } = bubble(single(makeMessage({ id: 5, kind: 'photo', text: '', media: [makeMedia({ id: 50 })] })));
    const content = container.querySelector('.message-content')!;
    expect(content.classList.contains('media-only')).toBe(true);
    expect(content.classList.contains('has-solid-background')).toBe(false);
    expect(container.querySelector('.MessageMeta.overlay')).toBeTruthy();
    fireEvent.click(container.querySelector('img')!);
    expect(store.viewer.value).toEqual({ chatId: 10, messageId: 5, mediaId: 50 });
  });

  it('renders stickers without a bubble', () => {
    const { container } = bubble(single(makeMessage({ kind: 'sticker', text: '', media: [makeMedia({ kind: 'sticker', mime: 'image/webp' })] })));
    expect(container.querySelector('.Message')!.classList.contains('no-bubble')).toBe(true);
    expect(container.querySelector('.has-solid-background')).toBeNull();
  });

  it('clears the long-press timer on unmount so it never fires afterwards', () => {
    vi.useFakeTimers();
    const onMenu = vi.fn();
    const { container, unmount } = bubble(single(makeMessage({ id: 1 })), onMenu);
    fireEvent.touchStart(container.querySelector('.message-content')!, { touches: [{ clientX: 1, clientY: 2 }] });
    unmount();
    vi.advanceTimersByTime(600);
    expect(onMenu).not.toHaveBeenCalled();
    vi.useRealTimers();
  });

  it('suppresses the native contextmenu briefly after a long-press already opened the menu', () => {
    vi.useFakeTimers();
    const onMenu = vi.fn();
    const { container } = bubble(single(makeMessage({ id: 1 })), onMenu);
    const content = container.querySelector('.message-content')!;
    fireEvent.touchStart(content, { touches: [{ clientX: 1, clientY: 2 }] });
    vi.advanceTimersByTime(500);
    expect(onMenu).toHaveBeenCalledTimes(1);
    fireEvent.contextMenu(content); // the mobile browser's own contextmenu, right after
    expect(onMenu).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(800);
    fireEvent.contextMenu(content); // a later, unrelated right-click still works
    expect(onMenu).toHaveBeenCalledTimes(2);
    vi.useRealTimers();
  });

  it('renders an album with its caption and reports the right-clicked tile', () => {
    const msgs = [1, 2].map((id) =>
      makeMessage({ id, kind: 'photo', media_group_id: 'g', text: id === 1 ? 'trip' : '', media: [makeMedia({ id: 100 + id })] }),
    );
    const onMenu = vi.fn();
    const { container } = bubble({ kind: 'album', key: '1', msgs, first: true, last: true }, onMenu);
    expect(container.querySelectorAll('.Album-tile')).toHaveLength(2);
    expect(container.querySelector('.text-content')!.textContent).toContain('trip');
    fireEvent.contextMenu(container.querySelectorAll('.Album-tile img')[1]);
    expect(onMenu.mock.calls[0][2].id).toBe(2);
  });
});

describe('MiddleColumn', () => {
  const setup = (messages: Message[]) => {
    const api = fakeApi({
      bots: vi.fn(async () => [makeBot({ id: 1, username: 'archive_bot' })]),
      chats: vi.fn(async () => [makeChat({ id: 10 })]),
      messages: vi.fn(async (_c: number, before = 0) => (before ? [] : messages)),
    });
    return api;
  };

  it('shows an empty hint without a chat', () => {
    renderWithStore(<MiddleColumn chatId={0} />);
    expect(screen.getByText('选择一个会话开始浏览存档')).toBeTruthy();
  });

  it('loads the newest page, shows the header and toggles shared media', async () => {
    const date = new Date(2026, 9, 4, 9, 0).getTime() / 1000;
    const api = setup([makeMessage({ id: 1, text: 'first', date }), makeMessage({ id: 2, text: 'second', date: date + 30 })]);
    const r = renderWithStore(<MiddleColumn chatId={10} />, api);
    await act(async () => {
      await r.store.loadBots();
      await r.store.loadChats();
    });
    expect(api.messages).toHaveBeenCalledWith(10, 0, 50);
    expect(await screen.findByText('second')).toBeTruthy();
    expect(screen.getAllByText('Alice').length).toBeGreaterThan(0);
    expect(screen.getByText('机器人 @archive_bot')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: '共享媒体' }));
    expect(r.store.sharedMediaOpen.value).toBe(true);
  });

  it('wraps each day in its own sticky-date group so headers do not stack on top of each other', async () => {
    const day1 = new Date(2026, 9, 3, 10, 0).getTime() / 1000;
    const day2 = new Date(2026, 9, 4, 9, 0).getTime() / 1000;
    const api = setup([makeMessage({ id: 1, text: 'first', date: day1 }), makeMessage({ id: 2, text: 'second', date: day2 })]);
    const { container } = renderWithStore(<MiddleColumn chatId={10} />, api);
    await screen.findByText('second');
    const groups = container.querySelectorAll('.message-date-group');
    expect(groups).toHaveLength(2);
    for (const g of groups) {
      expect(g.firstElementChild?.classList.contains('sticky-date')).toBe(true);
      expect(g.querySelector('.message-group')).toBeTruthy();
    }
  });

  it('loads older history when scrolled near the top', async () => {
    const page = Array.from({ length: 50 }, (_, i) => makeMessage({ id: 100 + i, text: `m${i}` }));
    const api = setup(page);
    const { container } = renderWithStore(<MiddleColumn chatId={10} />, api);
    await screen.findByText('m49');
    const list = container.querySelector('.MessageList')!;
    fireEvent.scroll(list);
    await waitFor(() => expect(api.messages).toHaveBeenLastCalledWith(10, 100, 50));
  });

  it('keeps loading older pages while the content does not fill the viewport', async () => {
    const pages: Record<number, Message[]> = {
      0: Array.from({ length: 50 }, (_, i) => makeMessage({ id: 200 + i })),
      200: Array.from({ length: 50 }, (_, i) => makeMessage({ id: 150 + i })),
      150: [],
    };
    const api = fakeApi({ messages: vi.fn(async (_c: number, before = 0) => pages[before] ?? []) });
    renderWithStore(<MiddleColumn chatId={10} />, api);
    await waitFor(() => expect(api.messages).toHaveBeenCalledTimes(3));
    expect(api.messages).toHaveBeenLastCalledWith(10, 150, 50);
  });

  it('deletes a message after confirmation from the context menu', async () => {
    const api = setup([makeMessage({ id: 1, text: 'bye' })]);
    const { container } = renderWithStore(<MiddleColumn chatId={10} />, api);
    await screen.findByText('bye');
    fireEvent.contextMenu(container.querySelector('.message-content')!);
    expect(screen.getByRole('menuitem', { name: '复制文本' })).toBeTruthy();
    fireEvent.click(screen.getByRole('menuitem', { name: '删除存档' }));
    await act(async () => {
      fireEvent.click(screen.getByText('删除'));
    });
    expect(api.deleteMessage).toHaveBeenCalledWith(1);
    await waitFor(() => expect(screen.queryByText('bye')).toBeNull());
  });

  it('offers download only for archived media', async () => {
    const api = setup([makeMessage({ id: 1, kind: 'document', text: '', media: [makeMedia({ id: 9, kind: 'document', file_name: 'a.txt' })] })]);
    const { container } = renderWithStore(<MiddleColumn chatId={10} />, api);
    await screen.findByText('a.txt');
    fireEvent.contextMenu(container.querySelector('.message-content')!);
    expect(screen.getByRole('menuitem', { name: '下载' })).toBeTruthy();
    expect(screen.queryByRole('menuitem', { name: '复制文本' })).toBeNull();
  });
});
