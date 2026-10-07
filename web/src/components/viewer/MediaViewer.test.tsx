import { act, fireEvent, screen } from '@testing-library/preact';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { fakeApi, makeChat, makeMedia, makeMessage } from '../../test/fixtures';
import { current } from '../../test/galleryStub';
import { renderWithStore } from '../../test/render';
import { MediaViewer } from './MediaViewer';

// PhotoSwipe and Vidstack don't run in jsdom: see test/galleryStub.tsx.
vi.mock('./Gallery', () => import('../../test/galleryStub'));


afterEach(() => {
  history.replaceState(null, '', '/');
});

function photoMsg(id: number, mediaId: number) {
  return makeMessage({ id, chat_id: 10, kind: 'photo', media: [makeMedia({ id: mediaId, role: 'main', kind: 'photo' })] });
}

async function setup() {
  const api = fakeApi({
    chatMedia: vi.fn(async () => [photoMsg(1, 101), photoMsg(2, 102), photoMsg(3, 103)]),
  });
  const r = renderWithStore(<MediaViewer />, api);
  act(() => {
    r.store.viewer.value = { chatId: 10, messageId: 2, mediaId: 102 };
  });
  await screen.findByRole('dialog', { name: '媒体查看器' });
  await screen.findByRole('button', { name: '关闭' });
  return r;
}

/** Replaces the real (asynchronous in jsdom) `history.back()` with a synchronous one that does
 * what the browser would: restores the entry below (dropping `viewer: true`) and fires
 * `popstate`. Used so UI-triggered closes (X, Escape, swipe-down) can be asserted immediately. */
function mockBack() {
  return vi.spyOn(history, 'back').mockImplementation(() => {
    const { viewer: _viewer, ...rest } = (history.state as Record<string, unknown> | null) ?? {};
    history.replaceState(rest, '', location.href);
    window.dispatchEvent(new PopStateEvent('popstate'));
  });
}

describe('MediaViewer in a bot timeline', () => {
  it('walks the bot media and titles each item with its own sender', async () => {
    const bobPhoto = makeMessage({ id: 3, chat_id: 11, kind: 'photo', media: [makeMedia({ id: 103, role: 'main', kind: 'photo' })] });
    const api = fakeApi({
      chats: vi.fn(async () => [
        makeChat({ id: 10, bot_id: 1 }),
        makeChat({ id: 11, bot_id: 1, sender: { tg_user_id: 7, first_name: 'Bob', last_name: '', username: '', has_avatar: false } }),
      ]),
      botMedia: vi.fn(async () => [bobPhoto, photoMsg(1, 101)]),
    });
    const r = renderWithStore(<MediaViewer />, api);
    await act(async () => {
      await r.store.loadChats();
    });
    act(() => {
      r.store.viewer.value = { chatId: -1, messageId: 1, mediaId: 101 };
    });
    await screen.findByText('1 / 2', { exact: false });
    expect(api.botMedia).toHaveBeenCalledWith(1, 'media', 0, expect.any(Number));
    expect(api.chatMedia).not.toHaveBeenCalled();
    expect(r.container.querySelector('.MediaViewer-name')!.textContent).toBe('Alice');
    fireEvent.click(screen.getByRole('button', { name: '下一个' }));
    expect(current(r.container).dataset.media).toBe('103');
    expect(r.container.querySelector('.MediaViewer-name')!.textContent).toBe('Bob');
  });
});

describe('MediaViewer with an explicit list', () => {
  const list = [
    { mediaId: 201, kind: 'photo', date: 1_790_000_000, text: '', entities: [] },
    { mediaId: 202, kind: 'video', date: 1_790_000_000, text: '', entities: [] },
    { mediaId: 203, kind: 'photo', date: 1_790_000_000, text: '', entities: [] },
  ];

  it('hands the gallery exactly the given items, titled by the list, without loading chat media', async () => {
    const api = fakeApi();
    const r = renderWithStore(<MediaViewer />, api);
    act(() => {
      r.store.viewer.value = { list, mediaId: 202, title: 'Sample' };
    });
    await screen.findByText('Sample');
    expect(screen.getByText('2 / 3')).toBeTruthy();
    expect(current(r.container).dataset.kind).toBe('video');
    await act(async () => {}); // flush effects: no chat-media request in list mode
    expect(api.chatMedia).not.toHaveBeenCalled();
    const back = mockBack();
    fireEvent.click(screen.getByRole('button', { name: '关闭' }));
    expect(back).toHaveBeenCalledTimes(1);
    expect(r.store.viewer.value).toBeNull();
    back.mockRestore();
  });
});

describe('toViewerItems via the chat loader', () => {
  it('carries the thumbnail and dimensions the gallery sizes slides with', async () => {
    const msg = makeMessage({
      id: 5,
      chat_id: 10,
      kind: 'video',
      media: [
        makeMedia({ id: 501, role: 'main', kind: 'video', width: 1920, height: 1080 }),
        makeMedia({ id: 502, role: 'thumb', kind: 'photo', state: 'done' }),
      ],
    });
    const api = fakeApi({ chatMedia: vi.fn(async () => [msg]) });
    const r = renderWithStore(<MediaViewer />, api);
    act(() => {
      r.store.viewer.value = { chatId: 10, messageId: 5, mediaId: 501 };
    });
    await screen.findByRole('button', { name: '关闭' });
    expect(current(r.container).dataset.thumb).toBe('502');
    expect(current(r.container).dataset.size).toBe('1920x1080');
  });
});

/** Reads the `viewer` marker off the current history entry, typed as the token string it now is
 * (not the plain `true` the viewer used to push). */
function viewerToken(): string | undefined {
  return (history.state as { viewer?: string } | null)?.viewer;
}

describe('MediaViewer history (system back closes only the viewer)', () => {
  it('pushes a history entry with a unique viewer token on open, preserving existing state, without navigating', async () => {
    history.replaceState({ fromChat: true }, '', '/chat/10/article/1');
    const before = history.length;
    await setup();
    expect(history.length).toBe(before + 1);
    const token = viewerToken();
    expect(typeof token).toBe('string');
    expect(token).not.toBe(''); // not a bare boolean marker any more
    expect(history.state).toEqual({ fromChat: true, viewer: token });
    expect(location.pathname).toBe('/chat/10/article/1');
  });

  it('closing via the X button goes back through history (pops the pushed entry) rather than closing immediately', async () => {
    const { store } = await setup();
    const noopBack = vi.spyOn(history, 'back').mockImplementation(() => {});
    fireEvent.click(screen.getByRole('button', { name: '关闭' }));
    expect(noopBack).toHaveBeenCalledTimes(1);
    expect(store.viewer.value).not.toBeNull(); // nothing closes it until the popstate actually arrives
    noopBack.mockRestore();

    const back = mockBack(); // now let a (synchronously simulated) back() actually pop the entry
    fireEvent.click(screen.getByRole('button', { name: '关闭' }));
    expect(back).toHaveBeenCalledTimes(1);
    expect(store.viewer.value).toBeNull();
    expect(viewerToken()).toBeUndefined();
    back.mockRestore();
  });

  it('a popstate past the pushed entry closes the viewer without navigating elsewhere', async () => {
    history.replaceState({ fromChat: true }, '', '/chat/10/article/1');
    const { store } = await setup();
    const token = viewerToken();
    expect(history.state).toEqual({ fromChat: true, viewer: token });
    await act(async () => {
      history.replaceState({ fromChat: true }, '', '/chat/10/article/1'); // what the browser restores on back: same path, no viewer
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    expect(store.viewer.value).toBeNull();
    expect(location.pathname).toBe('/chat/10/article/1'); // closing the viewer did not navigate
  });

  it('closed from elsewhere while its entry is on top, it pops that entry so back is not wasted', async () => {
    const { store } = await setup();
    const back = vi.spyOn(history, 'back').mockImplementation(() => {});
    act(() => {
      store.viewer.value = null;
    });
    expect(back).toHaveBeenCalledTimes(1);
    back.mockRestore();
  });

  it('closes on the first X press even when the entry below already carries an unrelated, stale viewer marker (e.g. left over from before a reload)', async () => {
    const { store } = await setup();
    // Simulates what a real history.back() lands on here: an older entry that still has its own
    // (different) viewer token from a previous open, instead of a clean "no viewer" state. A
    // plain boolean marker would see this as "still open" and require a second press.
    const back = vi.spyOn(history, 'back').mockImplementation(() => {
      history.replaceState({ viewer: 'stale-token-from-before-reload' }, '', location.href);
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    fireEvent.click(screen.getByRole('button', { name: '关闭' }));
    expect(back).toHaveBeenCalledTimes(1);
    expect(store.viewer.value).toBeNull();
    back.mockRestore();
  });
});

describe('MediaViewer on the media wall', () => {
  it('starts from the wall items, walks the whole wall with its filters and titles items by their chat', async () => {
    const bobPhoto = makeMessage({ id: 3, chat_id: 11, kind: 'photo', media: [makeMedia({ id: 103, role: 'main', kind: 'photo' })] });
    let release!: () => void;
    const gate = new Promise<void>((res) => (release = res));
    const api = fakeApi({
      chats: vi.fn(async () => [
        makeChat({ id: 10 }),
        makeChat({ id: 11, sender: { tg_user_id: 7, first_name: 'Bob', last_name: '', username: '', has_avatar: false } }),
      ]),
      allMedia: vi.fn(async () => {
        await gate;
        return [bobPhoto, photoMsg(2, 102), photoMsg(1, 101)];
      }),
    });
    const r = renderWithStore(<MediaViewer />, api);
    await act(async () => {
      await r.store.loadChats();
    });
    const seed = [{ mediaId: 102, kind: 'photo', date: 1, text: '', entities: [], chatId: 10 }];
    act(() => {
      r.store.viewer.value = { wall: { type: 'photo', source: 'private' }, seed, mediaId: 102 };
    });
    await screen.findByText('1 / 1', { exact: false });
    expect(api.allMedia).toHaveBeenCalledWith('photo', 'private', 0, expect.any(Number));
    await act(async () => release());
    await screen.findByText('2 / 3', { exact: false });
    expect(api.chatMedia).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: '下一个' }));
    expect(current(r.container).dataset.media).toBe('103');
    expect(r.container.querySelector('.MediaViewer-name')!.textContent).toBe('Bob');
  });
});
