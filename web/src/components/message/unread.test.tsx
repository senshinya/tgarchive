import { act, fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest';
import type { Api, PageParams } from '../../api/client';
import type { Message } from '../../api/types';
import { createStore, StoreContext } from '../../state/store';
import { fakeApi, makeChannelChat, makeChat, makeMessage } from '../../test/fixtures';
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

async function open(api: Api, chatId = 50) {
  const store = createStore(api, { chatsReloadDelay: 0 });
  await store.loadChats();
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
    const spy = vi.spyOn(Element.prototype, 'scrollIntoView').mockImplementation(function (this: Element) {
      scrolled.push(this);
    });
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
