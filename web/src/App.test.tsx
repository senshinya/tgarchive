import { act, fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { NETWORK_ERROR } from './api/client';
import { App } from './App';
import type { EventSourceLike } from './lib/sse';
import { route } from './lib/router';
import { createStore } from './state/store';
import { fakeApi, makeBot, makeChat, makeMedia, makeMessage } from './test/fixtures';

class FakeES implements EventSourceLike {
  readyState = 1;
  onopen: ((ev: Event) => unknown) | null = null;
  onerror: ((ev: Event) => unknown) | null = null;
  listeners = new Map<string, (ev: MessageEvent) => void>();
  closed = false;
  addEventListener(type: string, l: (ev: MessageEvent) => void) {
    this.listeners.set(type, l);
  }
  close() {
    this.closed = true;
  }
  emit(type: string, data: unknown) {
    this.listeners.get(type)?.(new MessageEvent(type, { data: JSON.stringify(data) }));
  }
}

afterEach(() => {
  history.replaceState(null, '', '/');
  route.value = { name: 'home' };
});

function setup(path = '/') {
  history.replaceState(null, '', path);
  let es!: FakeES;
  const api = fakeApi({
    bots: vi.fn(async () => [makeBot({ id: 1 })]),
    chats: vi.fn(async () => [makeChat({ id: 10 })]),
    messages: vi.fn(async () => [makeMessage({ id: 1, text: 'archived' })]),
    message: vi.fn(async (id: number) => makeMessage({ id, text: 'live!' })),
  });
  const store = createStore(api, { chatsReloadDelay: 0 });
  const utils = render(
    <App
      store={store}
      eventSource={() => {
        es = new FakeES();
        return es;
      }}
    />,
  );
  return { ...utils, api, store, es: () => es };
}

describe('App', () => {
  it('starts on the chat list with the left column open', async () => {
    const { container } = setup('/');
    expect(await screen.findByText('Alice')).toBeTruthy();
    expect(container.querySelector('#Main')!.className).toBe('left-column-open');
    expect(screen.getByText('选择一个会话开始浏览存档')).toBeTruthy();
  });

  it('deep-links into a chat and appends live messages from SSE', async () => {
    const { container, es, api } = setup('/chat/10');
    expect(await screen.findByText('archived')).toBeTruthy();
    expect(container.querySelector('#Main')!.className).toBe('');
    await act(async () => {
      es().emit('message.created', { chat_id: 10, message_id: 2 });
    });
    expect(await screen.findByText('live!')).toBeTruthy();
    expect(api.message).toHaveBeenCalledWith(2);
  });

  it('resyncs after the event stream reconnects', async () => {
    const { es, api } = setup('/');
    await screen.findByText('Alice');
    await act(async () => {
      es().onerror?.(new Event('error'));
      es().onopen?.(new Event('open'));
    });
    expect(api.bots).toHaveBeenCalledTimes(2);
    expect(api.chats).toHaveBeenCalledTimes(2);
  });

  it('toasts a network-error hint once when the SSE stream drops for good', async () => {
    const { store, es } = setup('/');
    await screen.findByText('Alice');
    await act(async () => {
      es().readyState = 2;
      es().onerror?.(new Event('error'));
    });
    expect(store.toast.value?.text).toBe(NETWORK_ERROR);
  });

  it('closes the media viewer on a route change (e.g. browser back)', async () => {
    const { store, api } = setup('/chat/10');
    await screen.findByText('archived');
    // A target the viewer can actually resolve, so it stays open on its own (ruling out its
    // unrelated "target not available" self-close as the reason it later closes).
    api.chatMedia = vi.fn(async () => [makeMessage({ id: 1, kind: 'photo', media: [makeMedia({ id: 30 })] })]);
    act(() => {
      store.viewer.value = { chatId: 10, messageId: 1, mediaId: 30 };
    });
    await screen.findByRole('dialog', { name: '媒体查看器' }); // proves it resolved a real item, not just "not null yet"
    await act(async () => {
      history.pushState(null, '', '/');
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    expect(store.viewer.value).toBeNull();
  });

  it('closes the shared media panel when switching to a different chat', async () => {
    const { store } = setup('/chat/10');
    await screen.findByText('archived');
    fireEvent.click(screen.getByRole('button', { name: '共享媒体' }));
    expect(store.sharedMediaOpen.value).toBe(true);
    await act(async () => {
      history.pushState(null, '', '/chat/11');
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    expect(store.sharedMediaOpen.value).toBe(false);
  });

  it('opens settings in the left column and the shared media panel on the right', async () => {
    const { container, store } = setup('/chat/10');
    await screen.findByText('archived');
    fireEvent.click(screen.getByRole('button', { name: '共享媒体' }));
    expect(container.querySelector('#Main')!.className).toBe('right-column-open');
    expect(store.sharedMediaOpen.value).toBe(true);
    expect(await screen.findByText('暂无媒体')).toBeTruthy();
    await act(async () => {
      history.pushState(null, '', '/settings');
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    expect(await screen.findByText('添加机器人')).toBeTruthy();
    expect(container.querySelector('#RightColumn')!.getAttribute('aria-hidden')).toBe('true');
  });

  it('shows a scrim over the left overlay that returns to the chat it covers', async () => {
    const { container } = setup('/chat/10');
    await screen.findByText('archived');
    expect(container.querySelector('#LeftScrim')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: '管理' }));
    expect(await screen.findByText('添加机器人')).toBeTruthy();
    const scrim = container.querySelector('#LeftScrim');
    expect(scrim).toBeTruthy();
    fireEvent.click(scrim!);
    expect(route.value).toEqual({ name: 'chat', chatId: 10 });
  });

  it('does not show the scrim when no chat has been opened yet', async () => {
    const { container } = setup('/');
    await screen.findByText('Alice');
    fireEvent.click(screen.getByRole('button', { name: '管理' }));
    expect(await screen.findByText('添加机器人')).toBeTruthy();
    expect(container.querySelector('#LeftScrim')).toBeNull();
  });

  it('opens the media wall and the stats from the chat list header', async () => {
    const { container, api } = setup('/');
    await screen.findByText('Alice');
    fireEvent.click(screen.getByRole('button', { name: '媒体墙' }));
    expect(location.pathname).toBe('/media');
    await waitFor(() => expect(container.querySelector('.MiddleHeader-title')?.textContent).toBe('媒体墙'));
    expect(container.querySelector('#Main')!.className).toBe('');
    await waitFor(() => expect(api.allMedia).toHaveBeenCalled());
    fireEvent.click(screen.getByRole('button', { name: '统计' }));
    expect(location.pathname).toBe('/stats');
    await waitFor(() => expect(container.querySelector('.MiddleHeader-title')?.textContent).toBe('统计'));
    await waitFor(() => expect(api.stats).toHaveBeenCalled());
  });

  it('closes the event stream on unmount', async () => {
    const { unmount, es } = setup('/');
    await screen.findByText('Alice');
    unmount();
    // Effect cleanups may run after the unmount call returns.
    await waitFor(() => expect(es().closed).toBe(true));
  });
});
