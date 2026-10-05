import { act, fireEvent, screen } from '@testing-library/preact';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { navigate, route } from '../../lib/router';
import { fakeApi, makeBot, makeChat, makeMessage } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { MiddleColumn } from './MiddleColumn';

afterEach(() => {
  route.value = { name: 'home' };
  history.replaceState(null, '', '/');
});

async function setup() {
  const api = fakeApi({ chats: vi.fn(async () => [makeChat({ id: 10 })]) });
  const r = renderWithStore(<MiddleColumn chatId={10} />, api);
  await act(async () => {
    await r.store.loadChats();
  });
  return r;
}

describe('MiddleColumn back button', () => {
  it('goes back in history (not a fresh "/" push) when the chat was opened from the chat list', async () => {
    await setup();
    // Simulates how ChatsPanel opens a chat: navigate(..., { fromList: true }).
    navigate({ name: 'chat', chatId: 10 }, { fromList: true });
    const back = vi.spyOn(history, 'back').mockImplementation(() => undefined);
    fireEvent.click(screen.getByRole('button', { name: '返回' }));
    expect(back).toHaveBeenCalledTimes(1);
    back.mockRestore();
  });

  it('navigates to home (does not call history.back()) when the chat was not opened from the list, e.g. a deep link', async () => {
    await setup();
    history.replaceState({ fromList: false }, '', '/chat/10');
    const back = vi.spyOn(history, 'back').mockImplementation(() => undefined);
    fireEvent.click(screen.getByRole('button', { name: '返回' }));
    expect(back).not.toHaveBeenCalled();
    expect(route.value).toEqual({ name: 'home' });
    expect(location.pathname).toBe('/');
    back.mockRestore();
  });

  it('navigates to home when there is no history state at all (e.g. a fresh deep link with no prior navigate() call)', async () => {
    await setup();
    history.replaceState(null, '', '/chat/10');
    const back = vi.spyOn(history, 'back').mockImplementation(() => undefined);
    fireEvent.click(screen.getByRole('button', { name: '返回' }));
    expect(back).not.toHaveBeenCalled();
    expect(route.value).toEqual({ name: 'home' });
    back.mockRestore();
  });
});

describe('MiddleColumn bot timeline', () => {
  it('shows the bot and its sender count, and labels each sender group', async () => {
    const api = fakeApi({
      bots: vi.fn(async () => [makeBot({ id: 1, name: 'Alpha' })]),
      chats: vi.fn(async () => [
        makeChat({ id: 10, bot_id: 1 }),
        makeChat({ id: 11, bot_id: 1, sender: { tg_user_id: 7, first_name: 'Bob', last_name: '', username: '', has_avatar: false } }),
      ]),
      botMessages: vi.fn(async () => [
        makeMessage({ id: 1, chat_id: 10, text: 'from alice' }),
        makeMessage({ id: 2, chat_id: 11, text: 'from bob' }),
        makeMessage({ id: 3, chat_id: 11, text: 'bob again' }),
      ]),
    });
    const r = renderWithStore(<MiddleColumn chatId={-1} />, api);
    await act(async () => {
      await r.store.loadBots();
      await r.store.loadChats();
    });
    await screen.findByText('from bob');
    expect(api.botMessages).toHaveBeenCalledWith(1, 0, 50);
    expect(screen.getByText('2 位发送人')).toBeTruthy();
    const names = [...r.container.querySelectorAll('.sender-title')].map((e) => e.textContent);
    expect(names).toEqual(['Alice', 'Bob']);
    expect(r.container.querySelectorAll('.message-group-avatar')).toHaveLength(2);
  });

  it('does not label senders in a single chat', async () => {
    const api = fakeApi({
      chats: vi.fn(async () => [makeChat({ id: 10 })]),
      messages: vi.fn(async () => [makeMessage({ id: 1, chat_id: 10, text: 'hi' })]),
    });
    const r = renderWithStore(<MiddleColumn chatId={10} />, api);
    await screen.findByText('hi');
    expect(r.container.querySelector('.sender-title')).toBeNull();
    expect(r.container.querySelector('.message-group-avatar')).toBeNull();
  });
});
