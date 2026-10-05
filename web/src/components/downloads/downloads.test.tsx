import { act, fireEvent, screen, waitFor } from '@testing-library/preact';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Downloads } from '../../api/types';
import { route } from '../../lib/router';
import { fakeApi, makeChat } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { DownloadsButton, DownloadsPanel } from './DownloadsPanel';

afterEach(() => {
  route.value = { name: 'home' };
  history.replaceState(null, '', '/');
  localStorage.removeItem('tgarchive.listMode');
});

const MB = 1024 * 1024;

const snapshot = (over: Partial<Downloads> = {}): Downloads => ({
  active: [{ media_id: 7, message_id: 3, chat_id: 10, kind: 'video', file_name: 'clip.mp4', size: 100 * MB, done: 25 * MB, total: 100 * MB, started_at: 1 }],
  queued: { count: 2, bytes: 50 * MB },
  failed: [{ media_id: 9, message_id: 4, chat_id: 10, kind: 'photo', file_name: '', size: 4, error: 'HTTP 500' }],
  speed: 2 * MB,
  ...over,
});

async function setupPanel(snap = snapshot()) {
  const api = fakeApi({
    chats: vi.fn(async () => [makeChat({ id: 10, bot_id: 1 })]),
    downloads: vi.fn(async () => snap),
  });
  const r = renderWithStore(<DownloadsPanel />, api);
  await act(async () => {
    await r.store.loadChats();
  });
  await screen.findByText('速度');
  return r;
}

describe('DownloadsPanel', () => {
  it('summarises speed, remaining and queue, and lists active and failed items', async () => {
    await setupPanel();
    expect(screen.getByText('2 MB/s')).toBeTruthy();
    expect(screen.getByText('125 MB')).toBeTruthy(); // 75 MB left of the active one + 50 MB queued
    expect(screen.getByText('进行中 · 1')).toBeTruthy();
    expect(screen.getByText('clip.mp4')).toBeTruthy();
    expect(screen.getByText('25 MB / 100 MB')).toBeTruthy();
    expect(screen.getAllByText('Alice')).toHaveLength(2);
    expect(screen.getByText('失败 · 1')).toBeTruthy();
    expect(screen.getByText('HTTP 500')).toBeTruthy();
  });

  it('follows live progress events', async () => {
    const r = await setupPanel();
    await act(async () => {
      await r.store.handleEvent({ type: 'download.progress', data: { items: [{ media_id: 7, done: 50 * MB, total: 100 * MB, started_at: 1 }], speed: 4 * MB } });
    });
    expect(screen.getByText('50 MB / 100 MB')).toBeTruthy();
    expect(screen.getByText('4 MB/s')).toBeTruthy();
  });

  it('retries a failed item', async () => {
    const r = await setupPanel();
    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    await waitFor(() => expect(r.api.retryMedia).toHaveBeenCalledWith(9));
  });

  it('opens the conversation of an item and asks it to jump to the message', async () => {
    const r = await setupPanel();
    fireEvent.click(screen.getByText('clip.mp4'));
    expect(route.value).toEqual({ name: 'chat', chatId: 10 });
    expect(r.store.jumpTo.value).toEqual({ key: 10, messageId: 3 });
    expect(history.state).toEqual({ fromList: true });
  });

  it('opens the bot timeline in bot mode', async () => {
    const r = await setupPanel();
    r.store.setListMode('bot');
    fireEvent.click(screen.getByText('clip.mp4'));
    expect(route.value).toEqual({ name: 'bot', botId: 1 });
  });

  it('says when nothing is happening', async () => {
    await setupPanel(snapshot({ active: [], failed: [], queued: { count: 0, bytes: 0 }, speed: 0 }));
    expect(screen.getByText('没有进行中的下载')).toBeTruthy();
  });
});

describe('DownloadsButton', () => {
  it('is hidden when idle and shows the total progress and a failure dot otherwise', async () => {
    const api = fakeApi({ downloads: vi.fn(async () => snapshot({ active: [], queued: { count: 0, bytes: 0 } })) });
    const r = renderWithStore(<DownloadsButton />, api);
    expect(r.container.querySelector('.DownloadsButton')).toBeNull();
    await act(async () => {
      await r.store.loadDownloads();
    });
    expect(r.container.querySelector('.DownloadsButton-dot')).toBeTruthy();
    expect(r.container.querySelector('.ProgressRing')).toBeNull();
    await act(async () => {
      await r.store.handleEvent({ type: 'download.progress', data: { items: [{ media_id: 7, done: 30, total: 100, started_at: 1 }], speed: 0 } });
    });
    expect(screen.getByRole('progressbar', { name: '总下载进度' }).getAttribute('aria-valuenow')).toBe('30');
    fireEvent.click(r.container.querySelector('.DownloadsButton')!);
    expect(route.value).toEqual({ name: 'downloads' });
  });
});
