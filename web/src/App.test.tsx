import { act, fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { App } from './App';
import type { EventSourceLike } from './lib/sse';
import { route } from './lib/router';
import { createStore } from './state/store';
import { fakeApi, makeBot, makeChat, makeMessage } from './test/fixtures';

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

  it('closes the event stream on unmount', async () => {
    const { unmount, es } = setup('/');
    await screen.findByText('Alice');
    unmount();
    // Effect cleanups may run after the unmount call returns.
    await waitFor(() => expect(es().closed).toBe(true));
  });
});
