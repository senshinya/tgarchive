import { fireEvent, screen, waitFor } from '@testing-library/preact';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { parseRoute, route, routePath } from '../../lib/router';
import { fakeApi, makeMedia, makeMessage } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { VideoLibrary } from './VideoLibrary';
import { invalidateLibraryCache } from './LibraryHome';
import { libraryParams, libraryQuery, resumeVideo } from './library';

export function makeVideoItem() {
  const media = makeMedia({
    id: 201,
    kind: 'video',
    mime: 'video/mp4',
    duration: 120,
    state: 'done',
  });
  const message = makeMessage({
    id: 21,
    kind: 'video',
    text: '山间日出',
    media: [media],
  });
  return {
    id: 21,
    message,
    title: '山间日出',
    description: '同一帖子的两段记录',
    date: 1790000000,
    added: 24,
    duration: 240,
    cover_media_id: 201,
    videos: [
      {
        message,
        media,
        progress: {
          position: 40,
          duration: 120,
          completed: false,
          updated_at: 1,
        },
      },
      {
        message: makeMessage({ ...message, id: 24 }),
        media: { ...media, id: 204 },
        progress: { position: 0, duration: 0, completed: false, updated_at: 0 },
      },
    ],
  };
}
const page = () => ({
  items: [makeVideoItem()],
  next: '',
  total_items: 1,
  total_videos: 2,
  collections: 1,
});
beforeEach(() => {
  history.replaceState(null, '', '/videos');
  route.value = { name: 'videos' };
});
describe('video library', () => {
  it('round trips list filters and deep detail/player routes', () => {
    expect(parseRoute('/videos?view=collections&q=%E5%B1%B1')).toEqual({
      name: 'videos',
      query: 'view=collections&q=%E5%B1%B1',
    });
    expect(routePath({ name: 'videos', query: 'q=%E5%B1%B1' })).toBe('/videos?q=%E5%B1%B1');
    expect(parseRoute('/videos/21')).toEqual({
      name: 'video-detail',
      itemId: 21,
    });
    expect(parseRoute('/videos/21/play/201')).toEqual({
      name: 'video-play',
      itemId: 21,
      mediaId: 201,
    });
  });
  it('normalizes untrusted URL filters and keeps Chinese query text', () => {
    expect(libraryParams('kind=bogus&chat_id=-2&sort=bogus&q=%E5%B1%B1')).toMatchObject({ kind: 'all', chat_id: 0, sort: 'new', q: '山' });
    expect(
      libraryParams(
        libraryQuery({
          ...libraryParams(''),
          view: 'collections',
          chat_id: 50,
        }),
      ),
    ).toMatchObject({ view: 'collections', chat_id: 50 });
    expect(resumeVideo(makeVideoItem()).media.id).toBe(201);
  });
  it('renders a full post as one collection card and opens its detail', async () => {
    const api = fakeApi({ videoLibrary: vi.fn(async () => page()) });
    renderWithStore(<VideoLibrary current={{ name: 'videos', query: 'kind=collection' }} />, api);
    await waitFor(() => expect(screen.getAllByText('山间日出').length).toBeGreaterThan(0));
    expect(screen.getByText('2 个视频')).toBeTruthy();
    fireEvent.click(screen.getByRole('link', { name: '查看：山间日出' }));
    expect(route.value).toEqual({ name: 'video-detail', itemId: 21 });
    expect(api.markRead).not.toHaveBeenCalled();
  });
  it('shows a recoverable error and retries the request', async () => {
    const request = vi.fn().mockRejectedValueOnce(new Error('暂时无法连接')).mockResolvedValue(page());
    renderWithStore(<VideoLibrary current={{ name: 'videos', query: 'kind=collection' }} />, fakeApi({ videoLibrary: request }));
    await screen.findByText('暂时无法连接');
    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    await screen.findByText('2 个视频');
  });
  it('detail preserves all videos and marks one watched without marking the chat read', async () => {
    const api = fakeApi({
      videoLibraryItem: vi.fn(async () => makeVideoItem()),
    });
    renderWithStore(<VideoLibrary current={{ name: 'video-detail', itemId: 21 }} />, api);
    await screen.findByRole('heading', { name: /^合集内容/ });
    fireEvent.click(screen.getAllByRole('button', { name: '标记为已看' })[0]);
    await waitFor(() =>
      expect(api.saveVideoProgress).toHaveBeenCalledWith(201, {
        position: 120,
        duration: 120,
        completed: true,
      }),
    );
    expect(api.markRead).not.toHaveBeenCalled();
  });
});

it('restores list scroll after navigating away and back', async () => {
  const api = fakeApi({ videoLibrary: vi.fn(async () => page()) });
  const first = renderWithStore(<VideoLibrary current={{ name: 'videos', query: 'q=scroll-restore' }} />, api);
  await screen.findByText('2 个视频');
  const scroll = first.container.querySelector('.LibraryScroll')!;
  scroll.scrollTop = 640;
  fireEvent.scroll(scroll);
  first.unmount();
  const second = renderWithStore(<VideoLibrary current={{ name: 'videos', query: 'q=scroll-restore' }} />, api);
  await screen.findByText('2 个视频');
  expect(second.container.querySelector('.LibraryScroll')!.scrollTop).toBe(640);
});

it('revalidates the cached list after changing a video from detail', async () => {
  const api = fakeApi({
    videoLibrary: vi.fn(async () => page()),
    videoLibraryItem: vi.fn(async () => makeVideoItem()),
  });
  const first = renderWithStore(<VideoLibrary current={{ name: 'videos', query: 'q=revalidate' }} />, api);
  await screen.findByText('2 个视频');
  first.unmount();
  const detail = renderWithStore(<VideoLibrary current={{ name: 'video-detail', itemId: 21 }} />, api);
  await screen.findByRole('heading', { name: /^合集内容/ });
  fireEvent.click(screen.getAllByRole('button', { name: '标记为已看' })[0]);
  await waitFor(() => expect(api.saveVideoProgress).toHaveBeenCalled());
  detail.unmount();
  renderWithStore(<VideoLibrary current={{ name: 'videos', query: 'q=revalidate' }} />, api);
  await waitFor(() => expect(api.videoLibrary).toHaveBeenCalledTimes(2));
});

it('revalidates every previously loaded page while retaining the list position', async () => {
  const items = Array.from({ length: 48 }, (_, i) => ({
    ...makeVideoItem(),
    id: i + 100,
    title: '山间日出 ' + i,
  }));
  const request = vi.fn(async (p?: import('../../api/types').VideoLibraryParams) => ({
    items: items.slice(p?.cursor ? 24 : 0, p?.cursor ? 48 : 24),
    next: p?.cursor ? '' : 'second',
    total_items: 48,
    total_videos: 96,
    collections: 48,
  }));
  const api = fakeApi({ videoLibrary: request });
  const first = renderWithStore(<VideoLibrary current={{ name: 'videos', query: 'q=all-pages' }} />, api);
  await waitFor(() => expect(first.container.querySelectorAll('.LibraryCard')).toHaveLength(24));
  fireEvent.click(screen.getByRole('button', { name: '加载更多' }));
  await waitFor(() => expect(first.container.querySelectorAll('.LibraryCard')).toHaveLength(48));
  const scroll = first.container.querySelector('.LibraryScroll')!;
  scroll.scrollTop = 1440;
  fireEvent.scroll(scroll);
  first.unmount();
  invalidateLibraryCache();
  const second = renderWithStore(<VideoLibrary current={{ name: 'videos', query: 'q=all-pages' }} />, api);
  await waitFor(() => expect(request).toHaveBeenCalledTimes(4));
  expect(second.container.querySelectorAll('.LibraryCard')).toHaveLength(48);
  expect(second.container.querySelector('.LibraryScroll')!.scrollTop).toBe(1440);
});

it('retries an initial failure when returning instead of caching it as an empty library', async () => {
  const request = vi.fn().mockRejectedValueOnce(new Error('首次请求失败')).mockResolvedValue(page());
  const api = fakeApi({ videoLibrary: request });
  const first = renderWithStore(<VideoLibrary current={{ name: 'videos', query: 'q=failed-cache' }} />, api);
  await screen.findByText('首次请求失败');
  first.unmount();
  renderWithStore(<VideoLibrary current={{ name: 'videos', query: 'q=failed-cache' }} />, api);
  await screen.findByText('2 个视频');
  expect(request).toHaveBeenCalledTimes(2);
});

it('revalidates an update that arrives during an in-flight page request', async () => {
  let finish: (value: ReturnType<typeof page>) => void = () => {};
  const request = vi.fn().mockImplementationOnce(() => new Promise(r => { finish = r; })).mockResolvedValue({ ...page(), items: [], total_items: 0, total_videos: 0, collections: 0 });
  const view = renderWithStore(<VideoLibrary current={{ name: 'videos', query: 'q=busy-refresh' }} />, fakeApi({ videoLibrary: request }));
  await waitFor(() => expect(request).toHaveBeenCalledTimes(1));
  await view.store.handleEvent({ type: 'message.deleted', data: { message_id: 21, chat_id: 10 } });
  finish(page());
  await waitFor(() => expect(request).toHaveBeenCalledTimes(2));
  await screen.findByRole('heading', { name: '没有找到匹配的视频' });
  expect(view.container.querySelectorAll('.LibraryCard')).toHaveLength(0);
});
