import { act, fireEvent, screen } from '@testing-library/preact';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { navigate, route } from '../../lib/router';
import { fakeApi, makeChat } from '../../test/fixtures';
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
