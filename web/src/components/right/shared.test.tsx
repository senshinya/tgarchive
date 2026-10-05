import { act, fireEvent, screen, waitFor } from '@testing-library/preact';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/client';
import type { Message } from '../../api/types';
import { fakeApi, makeMedia, makeMessage } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { MediaViewer } from '../viewer/MediaViewer';
import { SharedMedia } from './SharedMedia';

const oct = new Date(2026, 9, 3).getTime() / 1000;
const sep = new Date(2026, 8, 3).getTime() / 1000;

afterEach(() => {
  history.replaceState(null, '', '/');
});

/** The media viewer pushes a history entry on open (see MediaViewer.tsx) and closes by going
 * back through it; this replaces the real (asynchronous in jsdom) `history.back()` with a
 * synchronous one so closing can be asserted immediately. */
function mockBack() {
  return vi.spyOn(history, 'back').mockImplementation(() => {
    const { viewer: _viewer, ...rest } = (history.state as Record<string, unknown> | null) ?? {};
    history.replaceState(rest, '', location.href);
    window.dispatchEvent(new PopStateEvent('popstate'));
  });
}

describe('SharedMedia', () => {
  it('groups media by month and opens the viewer from a tile', async () => {
    const api = fakeApi({
      chatMedia: vi.fn(async () => [
        makeMessage({ id: 3, kind: 'photo', date: oct, media: [makeMedia({ id: 30 })] }),
        makeMessage({ id: 2, kind: 'video', date: sep, media: [makeMedia({ id: 20, kind: 'video', duration: 9 }), makeMedia({ id: 21, role: 'thumb' })] }),
      ]),
    });
    const { container, store } = renderWithStore(<SharedMedia chatId={10} />, api);
    expect(await screen.findByText('2026年10月')).toBeTruthy();
    expect(screen.getByText('2026年9月')).toBeTruthy();
    expect(api.chatMedia).toHaveBeenCalledWith(10, 'media', 0, 100);
    const imgs = [...container.querySelectorAll('.SharedMedia-tile img')].map((i) => i.getAttribute('src'));
    expect(imgs).toEqual(['/media/30', '/media/21']);
    expect(screen.getByText('0:09')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: '查看照片' }));
    expect(store.viewer.value).toEqual({ chatId: 10, messageId: 3, mediaId: 30 });
  });

  it('switches tabs and lists files and links', async () => {
    const api = fakeApi({
      chatMedia: vi.fn(async (_c: number, type: string) =>
        type === 'file'
          ? [makeMessage({ id: 5, kind: 'document', media: [makeMedia({ id: 50, kind: 'document', file_name: 'a.zip' })] })]
          : type === 'link'
            ? [makeMessage({ id: 6, text: 'see go.dev', entities: [{ type: 'url', offset: 4, length: 6 }] })]
            : [],
      ),
    });
    renderWithStore(<SharedMedia chatId={10} />, api);
    expect(await screen.findByText('暂无媒体')).toBeTruthy();
    fireEvent.click(screen.getByRole('tab', { name: '文件' }));
    expect(await screen.findByText('a.zip')).toBeTruthy();
    fireEvent.click(screen.getByRole('tab', { name: '链接' }));
    expect(await screen.findByText('go.dev')).toBeTruthy();
    expect(screen.getByText('https://go.dev/').closest('a')!.getAttribute('href')).toBe('https://go.dev/');
  });

  it('pages further when scrolled to the end', async () => {
    const full = Array.from({ length: 100 }, (_, i) => makeMessage({ id: 500 - i, kind: 'photo', date: oct, media: [makeMedia({ id: 1000 + i })] }));
    const api = fakeApi({ chatMedia: vi.fn(async (_c: number, _t: string, before = 0) => (before ? [] : full)) });
    const { container } = renderWithStore(<SharedMedia chatId={10} />, api);
    await waitFor(() => expect(container.querySelectorAll('.SharedMedia-tile')).toHaveLength(100));
    fireEvent.scroll(container.querySelector('.SharedMedia-content')!);
    await waitFor(() => expect(api.chatMedia).toHaveBeenLastCalledWith(10, 'media', 401, 100));
  });

  it('closes the panel', async () => {
    const { store } = renderWithStore(<SharedMedia chatId={10} />);
    store.sharedMediaOpen.value = true;
    fireEvent.click(screen.getByRole('button', { name: '关闭' }));
    expect(store.sharedMediaOpen.value).toBe(false);
  });

  it('blurs spoiler photos in the grid instead of showing them outright', async () => {
    const api = fakeApi({
      chatMedia: vi.fn(async () => [makeMessage({ id: 3, kind: 'photo', date: oct, extra: { spoiler: true }, media: [makeMedia({ id: 30 })] })]),
    });
    const { container } = renderWithStore(<SharedMedia chatId={10} />, api);
    await waitFor(() => expect(container.querySelector('.SharedMedia-tile img')).toBeTruthy());
    expect(container.querySelector('.SharedMedia-tile img')!.className).toBe('media-spoiler-blur');
  });
});

describe('MediaViewer', () => {
  const media = (): Message[] => [
    makeMessage({ id: 3, kind: 'photo', text: 'third', media: [makeMedia({ id: 30 })] }),
    makeMessage({ id: 2, kind: 'video', media: [makeMedia({ id: 20, kind: 'video' })] }),
    makeMessage({ id: 1, kind: 'photo', media: [makeMedia({ id: 10, state: 'failed' })] }),
  ];

  it('walks all archived media of the chat and closes with Escape', async () => {
    const api = fakeApi({ chatMedia: vi.fn(async () => media()) });
    const { store, container } = renderWithStore(<MediaViewer />, api);
    act(() => {
      store.viewer.value = { chatId: 10, messageId: 3, mediaId: 30 };
    });
    expect(await screen.findByText(/2 \/ 2/)).toBeTruthy();
    expect(container.querySelector('.MediaViewer-content img')!.getAttribute('src')).toBe('/media/30');
    expect(screen.getByText('third')).toBeTruthy();
    expect(screen.getByRole('link', { name: '下载' }).getAttribute('href')).toBe('/media/30?download=1');
    fireEvent.click(screen.getByRole('button', { name: '上一个' }));
    expect(container.querySelector('.MediaViewer-content video')!.getAttribute('src')).toBe('/media/20');
    expect(screen.queryByRole('button', { name: '上一个' })).toBeNull();
    fireEvent.keyDown(window, { key: 'ArrowRight' });
    expect(container.querySelector('.MediaViewer-content img')!.getAttribute('src')).toBe('/media/30');
    const back = mockBack();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(back).toHaveBeenCalledTimes(1);
    expect(store.viewer.value).toBeNull();
    back.mockRestore();
  });

  it('zooms photos within bounds', async () => {
    const api = fakeApi({ chatMedia: vi.fn(async () => media()) });
    const { store, container } = renderWithStore(<MediaViewer />, api);
    act(() => {
      store.viewer.value = { chatId: 10, messageId: 3, mediaId: 30 };
    });
    await screen.findByText(/2 \/ 2/);
    const img = () => container.querySelector('.MediaViewer-content img') as HTMLElement;
    expect((screen.getByRole('button', { name: '缩小' }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: '放大' }));
    expect(img().style.transform).toContain('scale(1.5)');
    fireEvent.dblClick(img());
    expect(img().style.transform).toContain('scale(1)');
  });

  it('closes itself when the target media is not available', async () => {
    const api = fakeApi({ chatMedia: vi.fn(async () => []) });
    const { store } = renderWithStore(<MediaViewer />, api);
    const back = mockBack();
    act(() => {
      store.viewer.value = { chatId: 10, messageId: 9, mediaId: 99 };
    });
    await waitFor(() => expect(store.viewer.value).toBeNull());
    expect(back).toHaveBeenCalledTimes(1);
    back.mockRestore();
  });

  it('toasts a background refresh error but keeps the viewer open when content was already seeded', async () => {
    const api = fakeApi({
      messages: vi.fn(async () => [makeMessage({ id: 3, kind: 'photo', media: [makeMedia({ id: 30 })] })]),
      chatMedia: vi.fn(async () => Promise.reject(new ApiError(500, 'boom', null))),
    });
    const { store } = renderWithStore(<MediaViewer />, api);
    await act(async () => {
      await store.refreshLatest(10);
    });
    act(() => {
      store.viewer.value = { chatId: 10, messageId: 3, mediaId: 30 };
    });
    await waitFor(() => expect(store.toast.value?.text).toBe('boom'));
    expect(store.viewer.value).not.toBeNull();
  });
});
