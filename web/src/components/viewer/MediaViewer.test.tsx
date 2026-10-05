import { act, fireEvent, screen } from '@testing-library/preact';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { fakeApi, makeMedia, makeMessage } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { MediaViewer } from './MediaViewer';

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
  return r;
}

/** Fires a pointerdown at `from`, then a pointerup at `to` (the same gesture a touch swipe produces). */
function swipe(el: Element, from: { x: number; y: number }, to: { x: number; y: number }) {
  fireEvent.pointerDown(el, { clientX: from.x, clientY: from.y, pointerId: 1 });
  fireEvent.pointerUp(el, { clientX: to.x, clientY: to.y, pointerId: 1 });
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

describe('MediaViewer swipe gestures', () => {
  it('swiping left (dx < -50, mostly horizontal) goes to the next item', async () => {
    const { container } = await setup();
    const content = container.querySelector('.MediaViewer-content')!;
    swipe(content, { x: 300, y: 300 }, { x: 200, y: 300 });
    expect(container.querySelector('img')!.getAttribute('src')).toBe('/media/103');
  });

  it('swiping right (dx > 50, mostly horizontal) goes to the previous item', async () => {
    const { container } = await setup();
    const content = container.querySelector('.MediaViewer-content')!;
    swipe(content, { x: 200, y: 300 }, { x: 300, y: 300 });
    expect(container.querySelector('img')!.getAttribute('src')).toBe('/media/101');
  });

  it('a drag under the 50px horizontal threshold does not navigate', async () => {
    const { container } = await setup();
    const content = container.querySelector('.MediaViewer-content')!;
    swipe(content, { x: 300, y: 300 }, { x: 270, y: 300 }); // dx = -30
    expect(container.querySelector('img')!.getAttribute('src')).toBe('/media/102');
  });

  it('swiping down more than 80px closes the viewer (via history.back(), since it pushed an entry on open)', async () => {
    const { container, store } = await setup();
    const back = mockBack();
    const content = container.querySelector('.MediaViewer-content')!;
    swipe(content, { x: 300, y: 200 }, { x: 300, y: 290 }); // dy = +90
    expect(back).toHaveBeenCalledTimes(1);
    expect(store.viewer.value).toBeNull();
    back.mockRestore();
  });

  it('a downward drag under the 80px threshold does not close', async () => {
    const { container, store } = await setup();
    const content = container.querySelector('.MediaViewer-content')!;
    swipe(content, { x: 300, y: 200 }, { x: 300, y: 260 }); // dy = +60
    expect(store.viewer.value).not.toBeNull();
    expect(container.querySelector('img')!.getAttribute('src')).toBe('/media/102');
  });

  it('a diagonal drag where the vertical delta dominates neither navigates nor closes below 80px', async () => {
    const { container, store } = await setup();
    const content = container.querySelector('.MediaViewer-content')!;
    swipe(content, { x: 300, y: 200 }, { x: 260, y: 270 }); // dx = -40, dy = +70, |dy| > |dx|, dy <= 80
    expect(store.viewer.value).not.toBeNull();
    expect(container.querySelector('img')!.getAttribute('src')).toBe('/media/102');
  });

  it('does not navigate while zoomed in — the same drag pans the image instead', async () => {
    const { container, store } = await setup();
    const img = container.querySelector('img')!;
    fireEvent.dblClick(img); // zoom 1 -> 2 (existing double-tap-to-zoom behaviour)
    expect(container.querySelector('img')!.classList.contains('zoomed')).toBe(true);
    const content = container.querySelector('.MediaViewer-content')!;
    swipe(content, { x: 300, y: 300 }, { x: 200, y: 300 }); // would be "next" at zoom 1
    expect(container.querySelector('img')!.getAttribute('src')).toBe('/media/102');
    expect(store.viewer.value).not.toBeNull();
  });
});

describe('MediaViewer with an explicit list', () => {
  const list = [
    { mediaId: 201, kind: 'photo', date: 1_790_000_000, text: '', entities: [] },
    { mediaId: 202, kind: 'video', date: 1_790_000_000, text: '', entities: [] },
    { mediaId: 203, kind: 'photo', date: 1_790_000_000, text: '', entities: [] },
  ];

  it('walks only the given items, titled by the list, without loading chat media', async () => {
    const api = fakeApi();
    const r = renderWithStore(<MediaViewer />, api);
    act(() => {
      r.store.viewer.value = { list, mediaId: 202, title: 'Sample' };
    });
    await screen.findByRole('dialog', { name: '媒体查看器' });
    expect(screen.getByText('Sample')).toBeTruthy();
    expect(screen.getByText(/2 \/ 3/)).toBeTruthy();
    expect(r.container.querySelector('.MediaViewer-content video')!.getAttribute('src')).toBe('/media/202');
    fireEvent.click(screen.getByRole('button', { name: '下一个' }));
    expect(r.container.querySelector('.MediaViewer-content img')!.getAttribute('src')).toBe('/media/203');
    expect(screen.queryByRole('button', { name: '下一个' })).toBeNull();
    fireEvent.keyDown(window, { key: 'ArrowLeft' });
    fireEvent.keyDown(window, { key: 'ArrowLeft' });
    expect(r.container.querySelector('.MediaViewer-content img')!.getAttribute('src')).toBe('/media/201');
    await act(async () => {}); // flush effects: no chat-media request in list mode
    expect(api.chatMedia).not.toHaveBeenCalled();
    const back = mockBack();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(back).toHaveBeenCalledTimes(1);
    expect(r.store.viewer.value).toBeNull();
    back.mockRestore();
  });
});

describe('MediaViewer history (system back closes only the viewer)', () => {
  it('pushes a history entry with viewer:true on open, preserving existing state, without navigating', async () => {
    history.replaceState({ fromChat: true }, '', '/chat/10/article/1');
    const before = history.length;
    await setup();
    expect(history.length).toBe(before + 1);
    expect(history.state).toEqual({ fromChat: true, viewer: true });
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
    expect((history.state as { viewer?: boolean } | null)?.viewer).toBeUndefined();
    back.mockRestore();
  });

  it('a popstate past the pushed entry closes the viewer without navigating elsewhere', async () => {
    history.replaceState({ fromChat: true }, '', '/chat/10/article/1');
    const { store } = await setup();
    expect(history.state).toEqual({ fromChat: true, viewer: true });
    await act(async () => {
      history.replaceState({ fromChat: true }, '', '/chat/10/article/1'); // what the browser restores on back: same path, no viewer
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    expect(store.viewer.value).toBeNull();
    expect(location.pathname).toBe('/chat/10/article/1'); // closing the viewer did not navigate
  });
});
