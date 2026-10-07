import { act, fireEvent, screen, waitFor } from '@testing-library/preact';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { route } from '../../lib/router';
import { localToday } from '../../lib/stats';
import { fakeApi, makeChat, makeStats } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { StatsView } from './StatsView';

afterEach(() => {
  history.replaceState(null, '', '/');
  route.value = { name: 'home' };
});

const stats = makeStats({
  totals: {
    messages: 12345,
    private_chats: 3,
    channel_chats: 2,
    media_files: 678,
    media_bytes: 3 * 1024 ** 3,
    db_bytes: 50 * 1024 ** 2,
    disk_free: 100 * 1024 ** 3,
    disk_total: 400 * 1024 ** 3,
  },
  daily: [{ day: localToday(), count: 7 }],
  monthly: [
    { month: '2026-08', messages: 10, media_bytes: 100 },
    { month: '2026-10', messages: 5, media_bytes: 0 },
  ],
  top_chats: [
    { chat_id: 10, messages: 900, media_bytes: 2048 },
    { chat_id: 99, messages: 10, media_bytes: 0 },
  ],
  media_kinds: [
    { kind: 'photo', count: 600, bytes: 1024 },
    { kind: 'other', count: 78, bytes: 10 },
  ],
  media_states: { done: 670, pending: 5, failed: 2, too_large: 1 },
  watches: [
    { watch_id: 3, chat_id: 50, title: 'News', daily: [{ day: localToday(), count: 2 }], hits: 40, scanned: 200, scan_hits: 5 },
    { watch_id: 4, chat_id: 51, title: 'Quiet', daily: [], hits: 0, scanned: 0, scan_hits: 0 },
    { watch_id: 5, chat_id: 52, title: 'Odd', daily: [], hits: 9, scanned: 2, scan_hits: 3 },
  ],
});

async function setup(api = fakeApi({ stats: vi.fn(async () => stats), chats: vi.fn(async () => [makeChat({ id: 10 })]) })) {
  const r = renderWithStore(<StatsView />, api);
  await act(async () => {
    await r.store.loadChats();
  });
  return r;
}

describe('StatsView', () => {
  it('asks for days cut in the browser’s time zone and shows the totals', async () => {
    const r = await setup();
    await screen.findByText('12,345');
    expect(r.api.stats).toHaveBeenCalledWith(-new Date().getTimezoneOffset());
    expect(screen.getByText('3 GB')).toBeTruthy();
    expect(screen.getByText('678 个文件')).toBeTruthy();
    expect(screen.getByText('私聊 3 · 频道 2')).toBeTruthy();
    expect(screen.getByText('剩余 100 GB / 共 400 GB')).toBeTruthy();
  });

  it('draws a year of days with today’s count', async () => {
    const r = await setup();
    await screen.findByText('12,345');
    const cells = r.container.querySelectorAll('.Heatmap-cell');
    expect(cells.length).toBeGreaterThanOrEqual(365);
    const today = r.container.querySelector(`.Heatmap-cell[data-day="${localToday()}"]`) as HTMLElement;
    expect(today.dataset.level).toBe('4');
    fireEvent.click(today);
    expect(screen.getByText(`${localToday()} · 7 条`)).toBeTruthy();
  });

  it('ranks chats by name and opens one on click', async () => {
    await setup();
    const alice = await screen.findByRole('button', { name: /Alice/ });
    expect(screen.getByRole('button', { name: /会话 99/ })).toBeTruthy();
    fireEvent.click(alice);
    expect(location.pathname).toBe('/chat/10');
  });

  it('shows each watch’s hit rate, or a dash before anything was scanned', async () => {
    await setup();
    await screen.findByText('News');
    expect(screen.getByText('命中率 2.5%')).toBeTruthy();
    expect(screen.getByText('命中率 —')).toBeTruthy();
    expect(screen.getByText('命中率 100%')).toBeTruthy();
  });

  it('links the failed downloads to the downloads panel', async () => {
    await setup();
    fireEvent.click(await screen.findByRole('button', { name: /失败/ }));
    expect(location.pathname).toBe('/downloads');
  });

  it('refreshes on demand and retries after an error', async () => {
    let fail = true;
    const api = fakeApi({
      stats: vi.fn(async () => {
        if (fail) throw new Error('boom');
        return stats;
      }),
    });
    const r = await setup(api);
    fireEvent.click(await screen.findByRole('button', { name: '重试' }));
    await waitFor(() => expect(api.stats).toHaveBeenCalledTimes(2));
    fail = false;
    fireEvent.click(await screen.findByRole('button', { name: '重试' }));
    await screen.findByText('12,345');
    fireEvent.click(screen.getByRole('button', { name: '刷新' }));
    await waitFor(() => expect(api.stats).toHaveBeenCalledTimes(4));
    expect(r.container.textContent).toContain('12,345');
  });
});
