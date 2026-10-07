import { act, fireEvent, screen, waitFor } from '@testing-library/preact';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, type Api } from '../../api/client';
import { route } from '../../lib/router';
import { fakeApi, makeChat, makeMessage } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { FavoritesView } from './FavoritesView';
import { TagDialog } from './TagDialog';

afterEach(() => {
  route.value = { name: 'home' };
  history.replaceState(null, '', '/');
});

const fav = (id: number, text: string, tags: { id: number; name: string }[] = []) => ({
  fav_id: id,
  message: makeMessage({ id, chat_id: 10, text, favorite: { at: 100 + id, tags } }),
});

function setup(over: Partial<Api> = {}) {
  const api = fakeApi({
    chats: vi.fn(async () => [makeChat({ id: 10 })]),
    favorites: vi.fn(async (tag = 0) => ({
      items: tag ? [fav(2, '第二条', [{ id: 7, name: '旅行' }])] : [fav(2, '第二条', [{ id: 7, name: '旅行' }]), fav(1, '第一条')],
      next: 0,
    })),
    tags: vi.fn(async () => [{ id: 7, name: '旅行', count: 1 }]),
    ...over,
  });
  const r = renderWithStore(<FavoritesView />, api);
  return { ...r, api };
}

describe('FavoritesView', () => {
  it('lists favorites newest first with their source and tags', async () => {
    const r = setup();
    await act(async () => {
      await r.store.loadChats();
    });
    await screen.findByText('第二条');
    const cards = r.container.querySelectorAll('.FavoriteCard');
    expect(cards.length).toBe(2);
    expect(cards[0].textContent).toContain('Alice');
    expect(cards[0].querySelector('.FavoriteCard-tags')?.textContent).toContain('旅行');
  });

  it('filters by tag', async () => {
    const r = setup();
    await screen.findByText('第一条');
    fireEvent.click(screen.getByRole('tab', { name: /旅行/ }));
    await waitFor(() => expect(r.api.favorites).toHaveBeenLastCalledWith(7, 0));
    await waitFor(() => expect(screen.queryByText('第一条')).toBeNull());
  });

  it('jumps to the message in its conversation', async () => {
    const r = setup();
    await screen.findByText('第一条');
    fireEvent.click(screen.getAllByRole('button', { name: '定位' })[1]);
    expect(r.store.jumpTo.value).toEqual({ key: 10, messageId: 1 });
    expect(route.value).toEqual({ name: 'chat', chatId: 10 });
  });

  it('reloads on favorites.updated', async () => {
    const r = setup();
    await screen.findByText('第一条');
    await act(async () => {
      await r.store.handleEvent({ type: 'favorites.updated', data: null });
    });
    await waitFor(() => expect(r.api.favorites).toHaveBeenCalledTimes(2));
  });

  it('drops a favorite deleted elsewhere and recounts the tags', async () => {
    const r = setup();
    await screen.findByText('第一条');
    await act(async () => {
      await r.store.handleEvent({ type: 'message.deleted', data: { chat_id: 10, message_id: 1 } });
    });
    expect(screen.queryByText('第一条')).toBeNull();
    expect(screen.getByText('第二条')).toBeTruthy();
    await waitFor(() => expect(r.api.tags).toHaveBeenCalledTimes(2));
  });

  it('refreshes a card whose message or media changed', async () => {
    const r = setup({
      message: vi.fn(async (id: number) => makeMessage({ id, chat_id: 10, text: `新的${id}`, favorite: { at: 1, tags: [] } })),
    });
    await screen.findByText('第一条');
    await act(async () => {
      await r.store.handleEvent({ type: 'media.updated', data: { media_id: 9, message_ids: [1, 99] } });
    });
    expect(await screen.findByText('新的1')).toBeTruthy();
    expect(r.api.message).toHaveBeenCalledTimes(1);
    await act(async () => {
      await r.store.handleEvent({ type: 'message.updated', data: { chat_id: 10, message_id: 2 } });
    });
    expect(await screen.findByText('新的2')).toBeTruthy();
  });

  it('drops a card whose message is gone when refreshed', async () => {
    const r = setup({ message: vi.fn(async () => Promise.reject(new ApiError(404, 'not found', null))) });
    await screen.findByText('第一条');
    await act(async () => {
      await r.store.handleEvent({ type: 'message.updated', data: { chat_id: 10, message_id: 1 } });
    });
    await waitFor(() => expect(screen.queryByText('第一条')).toBeNull());
  });

  it('explains how to add favorites when there are none', async () => {
    setup({ favorites: vi.fn(async () => ({ items: [], next: 0 })), tags: vi.fn(async () => []) });
    expect(await screen.findByText('还没有收藏的消息')).toBeTruthy();
  });
});

describe('TagDialog', () => {
  it('puts the cursor in the tag field when it opens', () => {
    renderWithStore(<TagDialog initial={[]} onSave={vi.fn(async () => undefined)} onClose={() => {}} />);
    expect(document.activeElement).toBe(screen.getByRole('textbox', { name: '添加标签' }));
  });

  it('adds, dedupes, suggests and removes tags, then saves them', async () => {
    const onSave = vi.fn(async () => undefined);
    renderWithStore(<TagDialog initial={['旅行']} onSave={onSave} onClose={() => {}} />, fakeApi({
      tags: vi.fn(async () => [
        { id: 1, name: '旅行', count: 1 },
        { id: 2, name: 'Food', count: 2 },
      ]),
    }));
    const input = screen.getByRole('textbox', { name: '添加标签' });
    fireEvent.input(input, { target: { value: 'fo' } });
    fireEvent.click(await screen.findByRole('button', { name: 'Food' }));
    fireEvent.input(input, { target: { value: '旅行' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    fireEvent.input(input, { target: { value: ' 新标签 ' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(screen.getAllByRole('button', { name: /移除标签/ }).map((b) => b.textContent)).toEqual(['旅行', 'Food', '新标签']);
    fireEvent.click(screen.getByRole('button', { name: '移除标签 Food' }));
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: '保存' }));
    });
    expect(onSave).toHaveBeenCalledWith(['旅行', '新标签']);
  });
});
