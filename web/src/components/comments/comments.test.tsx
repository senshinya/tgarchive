import { act, fireEvent, screen, waitFor } from '@testing-library/preact';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ArchiveEvent, Message, PostStats } from '../../api/types';
import { groupMessages } from '../../lib/grouping';
import { parseRoute, route, routePath } from '../../lib/router';
import { fakeApi, makeChannelChat, makeMedia, makeMessage } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { StoreContext } from '../../state/store';
import { MessageBubble } from '../message/MessageBubble';
import { MiddleColumn } from '../middle/MiddleColumn';
import { CommentButton, commentsLabel } from './CommentButton';

afterEach(() => {
  route.value = { name: 'home' };
  history.replaceState(null, '', '/');
});

const ann = { kind: 'user' as const, id: 11, name: 'Ann', photo: true };
const bo = { kind: 'user' as const, id: 12, name: 'Bo' };

function postStats(over: Partial<PostStats> = {}): PostStats {
  return { reactions: [], total: 0, views: 10, forwards: 0, replies: 3, comments: { recent: [ann, bo] }, ...over };
}

function post(over: Partial<Message> = {}): Message {
  return makeMessage({ id: 1, chat_id: 50, source: 'channel_watch', text: 'News', stats: postStats(), ...over });
}

function comment(id: number, from: typeof ann | typeof bo, text: string, over: Partial<Message> = {}): Message {
  return makeMessage({ id, chat_id: 50, source: 'channel_comment', text, extra: { from }, thread_root_id: 1, date: 1_790_000_000 + id, ...over });
}

function bubble(msg: Message, extra: { inThread?: boolean } = {}) {
  return renderWithStore(
    <MessageBubble bubble={{ kind: 'message', key: String(msg.id), msg, first: true, last: true }} sender={{ name: 'News', peerId: 500 }} convKey={50} onMenu={() => {}} {...extra} />,
  );
}

describe('comment bar', () => {
  it('labels the count, or invites a first comment', () => {
    expect(commentsLabel(0)).toBe('发表评论');
    expect(commentsLabel(7)).toBe('7 条评论');
    expect(commentsLabel(1530)).toBe('1.5K 条评论');
  });

  it('shows the newest commenters and opens the comments', () => {
    const { container } = renderWithStore(<CommentButton chatId={50} postId={1} stats={postStats()} />);
    const avatars = container.querySelectorAll('.recent-repliers .Avatar');
    expect(avatars).toHaveLength(2);
    expect(avatars[0].querySelector('img')!.getAttribute('src')).toBe('/avatars/users/11');
    expect(avatars[1].querySelector('img')).toBeNull();
    expect(container.querySelector('.icon-message')).toBeNull();
    expect(container.querySelector('.label')!.textContent).toBe('3 条评论');
    fireEvent.click(container.querySelector('.CommentButton')!);
    expect(route.value).toEqual({ name: 'comments', chatId: 50, messageId: 1 });
    expect(history.state).toMatchObject({ fromChat: true });
  });

  it('shows the speech bubble when nobody has commented', () => {
    const { container } = renderWithStore(<CommentButton chatId={50} postId={1} stats={postStats({ replies: 0, comments: { recent: [] } })} />);
    expect(container.querySelector('.icon-message')).toBeTruthy();
    expect(container.querySelector('.label')!.textContent).toBe('发表评论');
  });

  it('is disabled when the discussion cannot be read', () => {
    const { container } = renderWithStore(<CommentButton chatId={50} postId={1} stats={postStats({ comments: { recent: [], unreadable: true } })} />);
    const btn = container.querySelector('.CommentButton')!;
    expect(btn.classList.contains('disabled')).toBe(true);
    fireEvent.click(btn);
    expect(route.value).toEqual({ name: 'home' });
  });

  it('ends a channel post that takes comments, and not one that does not', () => {
    const { container } = bubble(post());
    expect(container.querySelector('.message-content.has-comments > .CommentButton')).toBeTruthy();
    expect(container.querySelector('.svg-appendix')).toBeTruthy();
    const none = bubble(post({ stats: postStats({ comments: undefined }) }));
    expect(none.container.querySelector('.CommentButton')).toBeNull();
  });

  it('puts the bar under a captionless photo inside a solid bubble', () => {
    const { container } = bubble(post({ kind: 'photo', text: '', media: [makeMedia()] }));
    const content = container.querySelector('.message-content')!;
    expect(content.classList.contains('has-solid-background')).toBe(true);
    expect(content.classList.contains('media-only')).toBe(false);
    expect(content.querySelector(':scope > .CommentButton')).toBeTruthy();
  });

  it('gives stickers the round button beside them', () => {
    const { container } = bubble(post({ kind: 'sticker', text: '', media: [makeMedia({ kind: 'sticker', mime: 'image/webp' })], origin_link: 'https://t.me/n/1' }));
    expect(container.querySelector('.message-content .CommentButton')).toBeNull();
    const round = container.querySelector('.message-action-buttons > .CommentButton.custom-shape')!;
    expect(round.getAttribute('data-cnt')).toBe('3');
    expect(container.querySelector('.message-action-buttons > a.message-action-button')).toBeTruthy();
  });

  it('is left out inside the comments', () => {
    const { container } = bubble(post(), { inThread: true });
    expect(container.querySelector('.CommentButton')).toBeNull();
  });
});

describe('comments', () => {
  it('groups comments by author', () => {
    const entries = groupMessages([comment(2, ann, 'a'), comment(3, ann, 'b'), comment(4, bo, 'c')]);
    const groups = entries.filter((e) => e.kind === 'group');
    expect(groups.map((g) => (g.kind === 'group' ? g.bubbles.length : 0))).toEqual([2, 1]);
  });

  it('has its own route', () => {
    expect(parseRoute('/chat/50/comments/1')).toEqual({ name: 'comments', chatId: 50, messageId: 1 });
    expect(routePath({ name: 'comments', chatId: 50, messageId: 1 })).toBe('/chat/50/comments/1');
  });

  it('shows the post, where the discussion starts, and the comments', async () => {
    const api = fakeApi({
      chats: vi.fn(async () => [makeChannelChat({ id: 50 })]),
      messages: vi.fn(async () => [post()]),
      comments: vi.fn(async () => [comment(2, ann, 'first!'), comment(3, ann, 'again'), comment(4, bo, 'hi', { reply_to_tg_message_id: 2, reply: { id: 2, kind: 'text', text: 'first!', extra: { from: ann } } })]),
    });
    const { container, store } = renderWithStore(<MiddleColumn chatId={50} commentsId={1} />, api);
    await waitFor(() => expect(container.textContent).toContain('hi'));
    expect(api.comments).toHaveBeenCalledWith(50, 1, { after: 1 }, 50);
    expect(api.refreshComments).toHaveBeenCalledWith(50, 1);
    const layer = container.querySelector('.CommentsLayer')!;
    expect(layer.querySelector('.CommentsHeader .MiddleHeader-title')!.textContent).toBe('3 条评论');
    expect(screen.getByText('讨论开始')).toBeTruthy();
    // The post has no comment bar here, and stands without an avatar.
    expect(layer.querySelector('.CommentButton')).toBeNull();
    expect(layer.querySelector('.comment-thread-top .message-group-avatar')).toBeNull();
    const groups = layer.querySelectorAll('.message-group.with-avatar');
    expect(groups).toHaveLength(2);
    expect(groups[0].querySelectorAll('.sender-title')).toHaveLength(1);
    expect(groups[0].querySelector('.sender-title')!.textContent).toBe('Ann');
    expect(groups[1].querySelector('.EmbeddedMessage .embedded-title')!.textContent).toBe('Ann');

    // New comments are appended when the server says so.
    (api.comments as ReturnType<typeof vi.fn>).mockResolvedValueOnce([comment(5, bo, 'late')]);
    await act(async () => {
      await store.handleEvent({ type: 'comments.updated', data: { chat_id: 50, root_id: 1 } } as ArchiveEvent);
    });
    await waitFor(() => expect(container.textContent).toContain('late'));
    expect(api.comments).toHaveBeenLastCalledWith(50, 1, { after: 4 }, 50);
  });

  it('covers the conversation, which stays as it was when they close', async () => {
    const api = fakeApi({
      chats: vi.fn(async () => [makeChannelChat({ id: 50 })]),
      messages: vi.fn(async () => [post()]),
      comments: vi.fn(async () => [comment(2, ann, 'first!')]),
    });
    const { container, rerender, store } = renderWithStore(<MiddleColumn chatId={50} />, api);
    void store.loadChats();
    await waitFor(() => expect(store.conv(50).loaded).toBe(true));
    const list = container.querySelector('.MessageList')!;
    rerender(<StoreContext.Provider value={store}><MiddleColumn chatId={50} commentsId={1} /></StoreContext.Provider>);
    await waitFor(() => expect(container.textContent).toContain('first!'));
    expect(container.querySelector('.CommentsLayer')).toBeTruthy();
    const loads = (api.messages as ReturnType<typeof vi.fn>).mock.calls.length;
    rerender(<StoreContext.Provider value={store}><MiddleColumn chatId={50} /></StoreContext.Provider>);
    expect(container.querySelector('.CommentsLayer')).toBeNull();
    // The very same list element: not rebuilt, so its scroll position is kept, and not reloaded.
    expect(container.querySelector('.MessageList')).toBe(list);
    expect((api.messages as ReturnType<typeof vi.fn>).mock.calls.length).toBe(loads);
  });

  it('says so when there are no comments', async () => {
    const api = fakeApi({ messages: vi.fn(async () => [post({ stats: postStats({ replies: 0, comments: { recent: [] } })})]) });
    renderWithStore(<MiddleColumn chatId={50} commentsId={1} />, api);
    await waitFor(() => expect(screen.getByText('暂无评论')).toBeTruthy());
    expect(screen.getByText('评论')).toBeTruthy();
  });

  it('keeps comments out of the conversation timeline', async () => {
    const api = fakeApi({ message: vi.fn(async () => comment(9, ann, 'x')) });
    const { store } = renderWithStore(<div />, api);
    store.conversations.value = { 50: { items: [post()], hasMore: false, hasNewer: false, loading: false, loaded: true, error: '' } };
    await act(async () => {
      await store.handleEvent({ type: 'message.updated', data: { chat_id: 50, message_id: 9 } } as ArchiveEvent);
    });
    expect(store.conv(50).items.map((m) => m.id)).toEqual([1]);
  });
});
