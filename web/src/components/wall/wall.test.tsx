import { act, fireEvent, screen, waitFor } from '@testing-library/preact';
import { describe, expect, it, vi } from 'vitest';
import { WALL_PAGE } from '../../api/client';
import type { Message } from '../../api/types';
import { fakeApi, makeMedia, makeMessage } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { MediaWall } from './MediaWall';

function photo(id: number, over: Partial<Message> = {}, state: 'done' | 'pending' = 'done') {
  return makeMessage({
    id,
    chat_id: 10,
    kind: 'photo',
    date: 1_790_000_000 - id * 3600,
    media: [makeMedia({ id: 100 + id, role: 'main', kind: 'photo', width: 800, height: 600, state })],
    ...over,
  });
}

function setup(allMedia = vi.fn(async (..._args: unknown[]) => [photo(3), photo(2), photo(1)] as Message[])) {
  const api = fakeApi({ allMedia: allMedia as never });
  const r = renderWithStore(<MediaWall />, api);
  return { ...r, allMedia };
}

const tiles = (c: Element) => c.querySelectorAll('.MediaWall-tile');

describe('MediaWall', () => {
  it('lays out every chat’s media in justified rows under month headings', async () => {
    const r = setup();
    await waitFor(() => expect(tiles(r.container)).toHaveLength(3));
    expect(r.allMedia).toHaveBeenCalledWith('all', 'all', 0, WALL_PAGE);
    expect(r.container.querySelector('.MediaWall-month-title')).toBeTruthy();
    expect(r.container.querySelectorAll('.MediaWall-row').length).toBeGreaterThan(0);
    expect(r.container.querySelector('.MiddleHeader-status')!.textContent).toBe('3 项');
  });

  it('reloads with the chosen filters', async () => {
    const r = setup();
    await waitFor(() => expect(tiles(r.container)).toHaveLength(3));
    fireEvent.click(screen.getByRole('tab', { name: '视频' }));
    await waitFor(() => expect(r.allMedia).toHaveBeenLastCalledWith('video', 'all', 0, WALL_PAGE));
    fireEvent.click(screen.getByRole('tab', { name: '频道' }));
    await waitFor(() => expect(r.allMedia).toHaveBeenLastCalledWith('video', 'channel', 0, WALL_PAGE));
  });

  it('loads older media near the bottom while pages are full', async () => {
    const full = Array.from({ length: WALL_PAGE }, (_, i) => photo(1000 - i));
    const r = setup(vi.fn(async (_t: unknown, _s: unknown, before: unknown) => (before ? [photo(5)] : full)));
    await waitFor(() => expect(tiles(r.container)).toHaveLength(WALL_PAGE));
    fireEvent.scroll(r.container.querySelector('.MediaWall')!);
    await waitFor(() => expect(r.allMedia).toHaveBeenLastCalledWith('all', 'all', 1000 - WALL_PAGE + 1, WALL_PAGE));
    await waitFor(() => expect(tiles(r.container)).toHaveLength(WALL_PAGE + 1));
    fireEvent.scroll(r.container.querySelector('.MediaWall')!);
    expect(r.allMedia).toHaveBeenCalledTimes(2);
  });

  it('keeps loading while the tiles do not fill the column', async () => {
    const full = Array.from({ length: WALL_PAGE }, (_, i) => photo(1000 - i));
    const r = setup(vi.fn(async (_t: unknown, _s: unknown, before: unknown) => (before ? [photo(5)] : full)));
    // jsdom has no layout: an unscrollable column, as on a tall screen.
    await waitFor(() => expect(tiles(r.container)).toHaveLength(WALL_PAGE + 1));
    expect(r.allMedia).toHaveBeenCalledTimes(2);
  });

  it('gives a month that shows up twice (a post archived late) its own section', async () => {
    const oct = 1_790_000_000;
    const r = setup(vi.fn(async () => [photo(3, { date: oct }), photo(2, { date: oct - 400 * 86400 }), photo(1, { date: oct - 3600 })]));
    await waitFor(() => expect(tiles(r.container)).toHaveLength(3));
    expect(r.container.querySelectorAll('.MediaWall-month')).toHaveLength(3);
  });

  it('opens the viewer on the wall with its filters, seeded with what is loaded', async () => {
    const r = setup();
    await waitFor(() => expect(tiles(r.container)).toHaveLength(3));
    fireEvent.click(screen.getByRole('tab', { name: '图片' }));
    await waitFor(() => expect(r.allMedia).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(tiles(r.container)).toHaveLength(3));
    fireEvent.click(tiles(r.container)[1]);
    const target = r.store.viewer.value as { wall: unknown; seed: { mediaId: number }[]; mediaId: number };
    expect(target.wall).toEqual({ type: 'photo', source: 'all' });
    expect(target.mediaId).toBe(102);
    expect(target.seed.map((s) => s.mediaId)).toEqual([101, 102, 103]);
  });

  it('keeps tiles in step with downloads and deletions', async () => {
    const r = setup(vi.fn(async () => [photo(2), photo(1, {}, 'pending')]));
    (r.api.message as ReturnType<typeof vi.fn>).mockImplementation(async (id: number) => photo(id));
    await waitFor(() => expect(tiles(r.container)).toHaveLength(2));
    expect((tiles(r.container)[1] as HTMLButtonElement).disabled).toBe(true);
    await act(async () => {
      await r.store.handleEvent({ type: 'media.updated', data: { media_id: 101, message_ids: [1] } });
    });
    await waitFor(() => expect((tiles(r.container)[1] as HTMLButtonElement).disabled).toBe(false));
    await act(async () => {
      await r.store.handleEvent({ type: 'message.deleted', data: { chat_id: 10, message_id: 2 } });
    });
    await waitFor(() => expect(tiles(r.container)).toHaveLength(1));
  });

  it('shows thumbnails rather than full photos when there are some', async () => {
    const withThumb = photo(2);
    withThumb.media.push(makeMedia({ id: 902, role: 'thumb', kind: 'photo', state: 'done' }));
    const r = setup(vi.fn(async () => [withThumb, photo(1)]));
    await waitFor(() => expect(tiles(r.container)).toHaveLength(2));
    const srcs = [...r.container.querySelectorAll('.MediaWall-tile img')].map((i) => i.getAttribute('src'));
    expect(srcs).toEqual(['/media/902', '/media/101']);
  });

  it('says so when there is nothing to show', async () => {
    setup(vi.fn(async () => []));
    expect(await screen.findByText('暂无媒体')).toBeTruthy();
  });
});
