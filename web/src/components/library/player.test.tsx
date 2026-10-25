import { fireEvent, screen, waitFor } from '@testing-library/preact';
import { beforeEach, expect, it, vi } from 'vitest';
import { fakeApi, makeMedia, makeMessage } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { LibraryPlayer } from './LibraryPlayer';

const harness = vi.hoisted(() => ({ players: [] as (HTMLElement & { currentTime: number })[], storages: [] as { getTime(): Promise<number | null> }[] }));
beforeEach(() => { harness.players.length = 0; harness.storages.length = 0; });
vi.mock('../viewer/videoSlide', () => ({ createVideoSlide: vi.fn((o: { storage: { getTime(): Promise<number | null> } }) => {
  const player = Object.assign(document.createElement('div'), { state: { duration: 120, ended: false }, currentTime: 0 });
  harness.players.push(player); harness.storages.push(o.storage);
  return { el: document.createElement('div'), player, setActive: vi.fn(), destroy: vi.fn() };
}) }));
function fixture(state: 'pending' | 'done') {
  const media = makeMedia({ id: 201, kind: 'video', mime: 'video/mp4', duration: 120, state });
  const message = makeMessage({ id: 21, kind: 'video', text: '山间日出', media: [media] });
  const video = { message, media, progress: { position: 10, duration: 120, completed: false, updated_at: 1 } };
  return { id: 21, message, title: '山间日出', description: '', date: 1790000000, added: 21, duration: 120, cover_media_id: 201, videos: [video] };
}
it('refreshes a pending video when the download finishes and disposes listeners on unmount', async () => {
  const load = vi.fn().mockResolvedValueOnce(fixture('pending')).mockResolvedValue(fixture('done'));
  const view = renderWithStore(<LibraryPlayer itemId={21} mediaId={201} />, fakeApi({ videoLibraryItem: load }));
  await screen.findByRole('heading', { name: '视频正在下载' });
  await view.store.handleEvent({ type: 'media.updated', data: { media_id: 201, message_ids: [21] } });
  await waitFor(() => expect(load).toHaveBeenCalledTimes(2));
  await waitFor(() => expect(screen.queryByRole('heading', { name: '视频正在下载' })).toBeNull());
  view.unmount();
  await view.store.handleEvent({ type: 'media.updated', data: { media_id: 201, message_ids: [21] } });
  expect(load).toHaveBeenCalledTimes(2);
});
it('retries a failed player at its latest position rather than the entry snapshot', async () => {
  renderWithStore(<LibraryPlayer itemId={21} mediaId={201} />, fakeApi({ videoLibraryItem: vi.fn(async () => fixture('done')) }));
  await waitFor(() => expect(harness.players).toHaveLength(1));
  const player = harness.players[0];
  player.dispatchEvent(new Event('playing'));
  player.currentTime = 65;
  player.dispatchEvent(new CustomEvent('time-update'));
  player.dispatchEvent(new Event('error'));
  fireEvent.click(await screen.findByRole('button', { name: '重试' }));
  await waitFor(() => expect(harness.storages).toHaveLength(2));
  expect(await harness.storages[1].getTime()).toBe(65);
});
