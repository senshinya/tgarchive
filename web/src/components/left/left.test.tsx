import { act, fireEvent, screen } from '@testing-library/preact';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { route } from '../../lib/router';
import { fakeApi, makeBot, makeChat } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { ChatsPanel } from './ChatsPanel';

afterEach(() => {
  route.value = { name: 'home' };
  history.replaceState(null, '', '/');
});

async function setup(bots = [makeBot({ id: 1, name: 'Alpha' }), makeBot({ id: 2, name: 'Beta', tg_bot_id: 888 })]) {
  const api = fakeApi({
    bots: vi.fn(async () => bots),
    chats: vi.fn(async () => [
      makeChat({ id: 10, bot_id: 1, last_text: 'hello' }),
      makeChat({ id: 11, bot_id: 2, last_kind: 'photo', last_text: '', sender: { tg_user_id: 7, first_name: 'Bob', last_name: 'Lee', username: '', has_avatar: true } }),
    ]),
  });
  const r = renderWithStore(<ChatsPanel />, api);
  await act(async () => {
    await r.store.loadBots();
    await r.store.loadChats();
  });
  return r;
}

describe('ChatsPanel', () => {
  it('lists chats with previews and bot prefixes in the 全部 view', async () => {
    const { container } = await setup();
    const items = container.querySelectorAll('.ChatItem');
    expect(items).toHaveLength(2);
    expect(items[0].textContent).toContain('Alice');
    expect(items[0].textContent).toContain('Alpha: hello');
    expect(items[1].textContent).toContain('Bob Lee');
    expect(items[1].textContent).toContain('Beta: 照片');
    expect(items[1].querySelector('img')!.getAttribute('src')).toBe('/avatars/senders/7');
  });

  it('filters by bot with the tab bar', async () => {
    const { container } = await setup();
    fireEvent.click(screen.getByRole('tab', { name: 'Beta' }));
    const items = container.querySelectorAll('.ChatItem');
    expect(items).toHaveLength(1);
    expect(items[0].textContent).not.toContain('Beta:');
    fireEvent.click(screen.getByRole('tab', { name: '全部' }));
    expect(container.querySelectorAll('.ChatItem')).toHaveLength(2);
  });

  it('hides the tab bar and prefixes with a single bot', async () => {
    const { container } = await setup([makeBot({ id: 1, name: 'Alpha' })]);
    expect(screen.queryByRole('tablist')).toBeNull();
    expect(container.textContent).not.toContain('Alpha:');
  });

  it('opens a chat and marks it selected', async () => {
    const { container } = await setup();
    fireEvent.click(container.querySelectorAll('.ChatItem')[1]);
    expect(route.value).toEqual({ name: 'chat', chatId: 11 });
    expect(location.pathname).toBe('/chat/11');
    expect(container.querySelectorAll('.ChatItem')[1].classList.contains('selected')).toBe(true);
  });

  it('shows all chats again, instead of staying stuck empty, after the filtered bot is purged', async () => {
    const { container, store, api } = await setup();
    fireEvent.click(screen.getByRole('tab', { name: 'Beta' }));
    expect(container.querySelectorAll('.ChatItem')).toHaveLength(1);

    // Bot 2 gets purged; the chats panel reloads (as BotSettings.remove() does).
    api.bots = vi.fn(async () => [makeBot({ id: 1, name: 'Alpha' })]);
    api.chats = vi.fn(async () => [makeChat({ id: 10, bot_id: 1 })]);
    await act(async () => {
      await store.loadBots();
      await store.loadChats();
    });
    expect(container.querySelectorAll('.ChatItem')).toHaveLength(1);
    expect(container.querySelectorAll('.ChatItem')[0].textContent).toContain('Alice');
  });

  it('shows the empty state and opens settings from the gear', async () => {
    const r = renderWithStore(<ChatsPanel />);
    await act(async () => {
      await r.store.loadChats();
    });
    expect(screen.getByText('暂无存档')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: '管理' }));
    expect(route.value).toEqual({ name: 'settings' });
  });
});
