import { act, fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest';
import type { Api, PageParams } from '../../api/client';
import type { Message } from '../../api/types';
import { createStore, StoreContext } from '../../state/store';
import { fakeApi, makeChannelChat, makeChat, makeMessage } from '../../test/fixtures';
import * as scroll from '../../lib/scroll';
import { MessageList } from './MessageList';

// jsdom lays nothing out: give the list a viewport so "at the bottom" means something.
const sizes = { scrollHeight: 2000, clientHeight: 500 };
const saved: Record<string, PropertyDescriptor | undefined> = {};
beforeAll(() => {
  for (const [k, v] of Object.entries(sizes)) {
    saved[k] = Object.getOwnPropertyDescriptor(HTMLElement.prototype, k);
    Object.defineProperty(HTMLElement.prototype, k, { configurable: true, get: () => v });
  }
});
afterAll(() => {
  for (const k of Object.keys(sizes)) if (saved[k]) Object.defineProperty(HTMLElement.prototype, k, saved[k]!);
});

const post = (id: number) => makeMessage({ id, chat_id: 50, text: `p${id}`, source: 'channel_watch' });
const range = (from: number, to: number) => Array.from({ length: to - from + 1 }, (_, i) => post(from + i));

async function open(api: Api, chatId = 50, before?: (store: ReturnType<typeof createStore>) => Promise<void> | void) {
  const store = createStore(api, { chatsReloadDelay: 0 });
  await store.loadChats();
  await before?.(store);
  const r = render(
    <StoreContext.Provider value={store}>
      <MessageList chatId={chatId} />
    </StoreContext.Provider>,
  );
  return { ...r, store };
}

const pages = (around: Message[], latest: Message[]) =>
  vi.fn(async (_c: number, _before = 0, _limit = 50, page: PageParams = {}) => (page.around ? around : latest));

describe('opening a channel with unread posts', () => {
  it('lands on the first unread post below a divider, and marks read only at the bottom', async () => {
    const scrolled: Element[] = [];
    const native = vi.spyOn(Element.prototype, 'scrollIntoView');
    const spy = vi.spyOn(scroll, 'scrollWithin').mockImplementation((_list, el) => void scrolled.push(el));
    const api = fakeApi({
      chats: vi.fn(async () => [makeChannelChat({ last_read_id: 120, unread: 3 })]),
      messages: pages(range(100, 123), range(74, 123)),
    });
    const { container } = await open(api);
    await screen.findByText('p123');
    expect(api.messages).toHaveBeenCalledWith(50, 0, 50, { around: 120 });
    const divider = container.querySelector('.unread-divider')!;
    expect(divider.textContent).toBe('以下为新消息');
    const next = divider.nextElementSibling;
    expect(next?.getAttribute('data-message-id')).toBe('121');
    expect(scrolled).toContain(divider);
    expect(api.markRead).not.toHaveBeenCalled();

    const list = container.querySelector('.MessageList') as HTMLElement;
    Object.defineProperty(list, 'scrollTop', { configurable: true, writable: true, value: 1500 });
    fireEvent.scroll(list);
    expect(api.markRead).toHaveBeenCalledWith(50, 123);
    // The divider stays where it was for this visit.
    expect(container.querySelector('.unread-divider')).toBe(divider);
    expect(native).not.toHaveBeenCalled(); // it would scroll the columns on phones too
    native.mockRestore();
    spy.mockRestore();
  });

  it('opens at the latest post without unread posts', async () => {
    const api = fakeApi({
      chats: vi.fn(async () => [makeChannelChat({ last_read_id: 123, unread: 0 })]),
      messages: pages([], range(100, 123)),
    });
    const { container } = await open(api);
    await screen.findByText('p123');
    expect(api.messages).toHaveBeenCalledWith(50, 0, 50);
    expect(vi.mocked(api.messages).mock.calls.some((c) => c[3]?.around)).toBe(false);
    expect(container.querySelector('.unread-divider')).toBeNull();
  });

  it('starts from the oldest post when nothing was read yet', async () => {
    const api = fakeApi({
      chats: vi.fn(async () => [makeChannelChat({ last_read_id: 0, unread: 5 })]),
      messages: pages(range(1, 5), range(1, 5)),
    });
    const { container } = await open(api);
    await screen.findByText('p5');
    expect(api.messages).toHaveBeenCalledWith(50, 0, 50, { around: 1 });
    const first = container.querySelector('.unread-divider')?.nextElementSibling;
    expect(first?.getAttribute('data-message-id')).toBe('1');
  });

  it('lets a jump to a message already loaded win over the unread posts', async () => {
    const scrolled: Element[] = [];
    const native = vi.spyOn(Element.prototype, 'scrollIntoView');
    const spy = vi.spyOn(scroll, 'scrollWithin').mockImplementation((_list, el) => void scrolled.push(el));
    const api = fakeApi({
      chats: vi.fn(async () => [makeChannelChat({ last_read_id: 120, unread: 3 })]),
      messages: pages(range(100, 123), range(74, 123)),
    });
    await open(api, 50, async (store) => {
      await store.refreshLatest(50); // posts kept from an earlier visit
      store.jumpTo.value = { key: 50, messageId: 110 };
    });
    await waitFor(() => expect(scrolled.some((el) => el.getAttribute('data-message-id') === '110')).toBe(true));
    await act(async () => {});
    expect(vi.mocked(api.messages).mock.calls.some((c) => c[3]?.around)).toBe(false);
    expect(scrolled.some((el) => el.classList.contains('unread-divider'))).toBe(false);
    expect(native).not.toHaveBeenCalled(); // it would scroll the columns on phones too
    native.mockRestore();
    spy.mockRestore();
  });

  it('marks nothing read while the unread posts load, and falls back to the usual list if they fail', async () => {
    let fail: (e: Error) => void = () => {};
    const api = fakeApi({
      chats: vi.fn(async () => [makeChannelChat({ last_read_id: 120, unread: 3 })]),
      messages: vi.fn(async (_c: number, _b = 0, _l = 50, page: PageParams = {}) =>
        page.around ? new Promise<Message[]>((_, reject) => (fail = reject)) : range(74, 123),
      ),
    });
    const { container } = await open(api, 50, (store) => store.refreshLatest(50));
    const list = container.querySelector('.MessageList') as HTMLElement;
    Object.defineProperty(list, 'scrollTop', { configurable: true, writable: true, value: 1500 });
    fireEvent.scroll(list);
    expect(api.markRead).not.toHaveBeenCalled();
    await act(async () => fail(new Error('boom')));
    await screen.findByText('重试');
    // Effects run after paint: keep scrolling until the list is back to normal.
    await waitFor(() => {
      fireEvent.scroll(list);
      expect(api.markRead).toHaveBeenCalledWith(50, 123);
    });
  });

  it('keeps private chats on the latest message', async () => {
    const api = fakeApi({
      chats: vi.fn(async () => [makeChat({ id: 10, last_read_id: 1, unread: 4 })]),
      messages: pages([], [makeMessage({ id: 1, text: 'hi' })]),
    });
    await open(api, 10);
    await screen.findByText('hi');
    expect(api.messages).toHaveBeenCalledWith(10, 0, 50);
    expect(api.refreshPostStats).not.toHaveBeenCalled();
  });
});

describe('opening a channel', () => {
  it('asks for fresh counters of the posts it shows', async () => {
    const api = fakeApi({
      chats: vi.fn(async () => [makeChannelChat()]),
      messages: pages([], [...range(1, 3), makeMessage({ id: 4, chat_id: 50, text: 'fetched', source: 'userbot_fetch' })]),
    });
    await open(api);
    await screen.findByText('fetched');
    await waitFor(() => expect(api.refreshPostStats).toHaveBeenCalledWith(50, [1, 2, 3]));
    await act(async () => {});
    expect(api.refreshPostStats).toHaveBeenCalledTimes(1);
  });
});
