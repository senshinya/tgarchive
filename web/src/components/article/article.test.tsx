import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ArticleNode, ArticleSummary, Message } from '../../api/types';
import { App } from '../../App';
import type { EventSourceLike } from '../../lib/sse';
import { route } from '../../lib/router';
import { createStore } from '../../state/store';
import { fakeApi, makeArticle, makeArticleMedia, makeBot, makeChat, makeMessage } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { MessageBubble } from '../message/MessageBubble';
import { ArticleCard } from './ArticleCard';
import { ArticleReader } from './ArticleReader';

afterEach(() => {
  history.replaceState(null, '', '/');
  route.value = { name: 'home' };
});

function summary(over: Partial<ArticleSummary> = {}): ArticleSummary {
  return { state: 'fetched', error: '', title: 'Sample', description: 'A sample article', author_name: 'Anon', url: 'https://telegra.ph/Sample-10-05', ...over };
}

function linkMsg(article: ArticleSummary): Message {
  return makeMessage({ id: 1, chat_id: 10, text: 'https://telegra.ph/Sample-10-05', article });
}

describe('ArticleCard', () => {
  it('shows progress while queued or fetching', () => {
    renderWithStore(<ArticleCard msg={linkMsg(summary({ state: 'queued', title: '' }))} />);
    expect(screen.getByText('Telegraph')).toBeTruthy();
    expect(screen.getByText('正在存档文章…')).toBeTruthy();
    expect(screen.queryByRole('button')).toBeNull();
  });

  it('shows the error and the original link when archiving failed', () => {
    renderWithStore(<ArticleCard msg={linkMsg(summary({ state: 'failed', error: '文章不存在', title: '' }))} />);
    expect(screen.getByText('存档失败：文章不存在')).toBeTruthy();
    expect(screen.getByRole('link').getAttribute('href')).toBe('https://telegra.ph/Sample-10-05');
  });

  it('drops an unsafe original link', () => {
    const { container } = renderWithStore(<ArticleCard msg={linkMsg(summary({ state: 'failed', error: 'x', url: 'javascript:alert(1)' }))} />);
    expect(container.querySelector('a')).toBeNull();
  });

  it('shows title, description and cover once fetched, and opens the reader with a history entry', () => {
    history.replaceState({ fromList: true }, '', '/chat/10');
    const { container } = renderWithStore(<ArticleCard msg={linkMsg(summary({ image_media_id: 7 }))} />);
    expect(screen.getByText('Sample')).toBeTruthy();
    expect(screen.getByText('A sample article')).toBeTruthy();
    expect(container.querySelector('.ArticleCard-image')!.getAttribute('src')).toBe('/media/7');
    const before = history.length;
    fireEvent.click(screen.getByRole('button'));
    expect(location.pathname).toBe('/chat/10/article/1');
    expect(history.length).toBe(before + 1);
    expect(history.state).toEqual({ fromList: false, fromChat: true });
    expect(route.value).toEqual({ name: 'article', chatId: 10, messageId: 1 });
  });

  it('renders inside the message bubble under the text', () => {
    const msg = linkMsg(summary());
    const { container } = renderWithStore(
      <MessageBubble bubble={{ kind: 'message', key: '1', msg, first: true, last: true }} sender={{ name: 'Alice', peerId: 42 }} onMenu={vi.fn()} />,
    );
    const text = container.querySelector('.text-content')!;
    expect(text.querySelector('.ArticleCard')).toBeTruthy();
    expect(text.textContent!.startsWith('https://telegra.ph/Sample-10-05')).toBe(true);
  });
});

const everyNode: ArticleNode[] = [
  { tag: 'p', children: ['Plain ', { tag: 'strong', children: ['bold'] }, ' ', { tag: 'b', children: ['b'] }, ' ', { tag: 'em', children: ['em'] }, ' ', { tag: 'i', children: ['i'] }, ' ', { tag: 'u', children: ['u'] }, ' ', { tag: 's', children: ['s'] }, ' ', { tag: 'code', children: ['code'] }, { tag: 'br' }, 'after break'] },
  { tag: 'h3', children: ['Heading 3'] },
  { tag: 'h4', children: ['Heading 4'] },
  { tag: 'blockquote', children: ['A quote'] },
  { tag: 'aside', children: ['A pull quote'] },
  { tag: 'ul', children: [{ tag: 'li', children: ['bullet'] }] },
  { tag: 'ol', children: [{ tag: 'li', children: ['numbered'] }] },
  { tag: 'pre', children: ['let x = 1;'] },
  { tag: 'hr' },
  { tag: 'p', children: [{ tag: 'a', attrs: { href: 'https://example.com/x' }, children: ['safe link'] }, ' ', { tag: 'a', attrs: { href: 'javascript:alert(1)' }, children: ['bad link'] }] },
  { tag: 'figure', children: [{ tag: 'img', attrs: { 'data-media-id': '201', 'data-src': 'https://telegra.ph/file/a.jpg' } }, { tag: 'figcaption', children: ['A caption'] }] },
  { tag: 'video', attrs: { 'data-media-id': '202', 'data-src': 'https://cdn.example.com/v.mp4' } },
  { tag: 'img', attrs: { 'data-media-id': '203', 'data-src': 'https://telegra.ph/file/c.jpg' } },
  { tag: 'img', attrs: { 'data-media-id': '204', 'data-src': 'https://telegra.ph/file/d.jpg' } },
  { tag: 'img', attrs: { 'data-src': 'javascript:alert(1)' } },
  { tag: 'figure', children: [{ tag: 'embed', attrs: { href: 'https://www.youtube.com/watch?v=dQw4w9WgXcQ', src: 'https://telegra.ph/embed/youtube?url=x' } }] },
  { tag: 'script', children: ['unwrapped text'] },
];

const everyMedia = [
  makeArticleMedia({ id: 201, kind: 'photo', state: 'done' }),
  makeArticleMedia({ id: 202, kind: 'video', state: 'done', mime: 'video/mp4' }),
  makeArticleMedia({ id: 203, kind: 'photo', state: 'failed' }),
  makeArticleMedia({ id: 204, kind: 'photo', state: 'pending' }),
];

async function openReader(over: Parameters<typeof makeArticle>[0] = {}) {
  const api = fakeApi({ article: vi.fn(async () => makeArticle({ content: everyNode, media: everyMedia, ...over })) });
  const r = renderWithStore(<ArticleReader chatId={10} messageId={1} />, api);
  await screen.findByRole('heading', { level: 1, name: 'Sample' });
  return r;
}

describe('ArticleReader', () => {
  it('renders every node type without raw HTML', async () => {
    const { container, api } = await openReader();
    expect(api.article).toHaveBeenCalledWith(1);
    const body = container.querySelector('.ArticleReader-body')!;
    for (const sel of ['p strong', 'p em', 'p u', 'p s', 'p code', 'p br', 'h3', 'h4', 'blockquote', 'aside', 'ul li', 'ol li', 'pre', 'hr', 'figure figcaption']) {
      expect(body.querySelector(sel), sel).toBeTruthy();
    }
    expect(body.querySelectorAll('strong')).toHaveLength(2); // <b> renders as <strong>
    expect(body.querySelector('script')).toBeNull();
    expect(body.textContent).toContain('unwrapped text');
    expect(screen.getByText('safe link').closest('a')!.getAttribute('href')).toBe('https://example.com/x');
    expect(screen.getByText('bad link').closest('a')).toBeNull();
    expect(screen.getByText('Anon').closest('a')!.getAttribute('href')).toBe('https://t.me/anon');
    expect(screen.getByRole('link', { name: '在 Telegraph 打开' }).getAttribute('href')).toBe('https://telegra.ph/Sample-10-05');
  });

  it('shows done media, placeholders with the original link, and an embed card', async () => {
    const { container } = await openReader();
    expect(container.querySelector('.ArticleMedia-photo img')!.getAttribute('src')).toBe('/media/201');
    expect(container.querySelector('video.ArticleMedia')!.getAttribute('src')).toBe('/media/202');
    const placeholders = container.querySelectorAll('.ArticleMedia-placeholder');
    expect(placeholders).toHaveLength(3);
    expect(placeholders[0].textContent).toContain('下载失败');
    expect(placeholders[0].querySelector('a')!.getAttribute('href')).toBe('https://telegra.ph/file/c.jpg');
    expect(placeholders[1].textContent).toContain('正在下载…');
    expect(placeholders[1].querySelector('button')).toBeNull();
    expect(placeholders[2].querySelector('a')).toBeNull(); // javascript: original link dropped
    const embed = container.querySelector('a.ArticleEmbed')!;
    expect(embed.getAttribute('href')).toBe('https://www.youtube.com/watch?v=dQw4w9WgXcQ');
    expect(embed.textContent).toContain('YouTube');
  });

  it('retries a failed image through the media retry endpoint', async () => {
    const { container, api } = await openReader();
    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    await waitFor(() => expect(api.retryMedia).toHaveBeenCalledWith(203));
    await waitFor(() => expect(container.querySelectorAll('.ArticleMedia-placeholder')[0].textContent).toContain('正在下载…'));
  });

  it('opens the media viewer on this article’s photos and videos only', async () => {
    const { store } = await openReader();
    fireEvent.click(screen.getByRole('button', { name: '查看图片' }));
    const t = store.viewer.value;
    expect(t && 'list' in t ? { ids: t.list.map((i) => i.mediaId), kinds: t.list.map((i) => i.kind), mediaId: t.mediaId, title: t.title } : t).toEqual({
      ids: [201, 202],
      kinds: ['photo', 'video'],
      mediaId: 201,
      title: 'Sample',
    });
  });

  it('shows the load error', async () => {
    const api = fakeApi({ article: vi.fn(async () => Promise.reject(new Error('not found'))) });
    renderWithStore(<ArticleReader chatId={10} messageId={1} />, api);
    expect(await screen.findByText('not found')).toBeTruthy();
  });

  it('back button goes back through history when opened from the chat, else replaces with the chat', async () => {
    const back = vi.spyOn(history, 'back').mockImplementation(() => {});
    history.pushState({ fromList: false, fromChat: true }, '', '/chat/10/article/1');
    await openReader();
    fireEvent.click(screen.getByRole('button', { name: '返回' }));
    expect(back).toHaveBeenCalledTimes(1);
    back.mockRestore();
  });

  it('back button from a deep link replaces the entry with the chat', async () => {
    history.replaceState(null, '', '/chat/10/article/1');
    const before = history.length;
    await openReader();
    fireEvent.click(screen.getByRole('button', { name: '返回' }));
    expect(location.pathname).toBe('/chat/10');
    expect(history.length).toBe(before);
  });
});

class FakeES implements EventSourceLike {
  readyState = 1;
  onopen: ((ev: Event) => unknown) | null = null;
  onerror: ((ev: Event) => unknown) | null = null;
  addEventListener() {}
  close() {}
}

describe('ArticleReader in the app', () => {
  it('opens from the card and closes on the system back button (popstate)', async () => {
    history.replaceState({ fromList: true }, '', '/chat/10');
    const api = fakeApi({
      bots: vi.fn(async () => [makeBot({ id: 1 })]),
      chats: vi.fn(async () => [makeChat({ id: 10 })]),
      messages: vi.fn(async () => [linkMsg(summary())]),
    });
    const store = createStore(api, { chatsReloadDelay: 0 });
    const { container } = render(<App store={store} eventSource={() => new FakeES()} />);
    fireEvent.click(await screen.findByRole('button', { name: /Sample/ }));
    expect(await screen.findByRole('dialog', { name: '文章' })).toBeTruthy();
    expect(container.querySelector('#MiddleColumn .ArticleReader')).toBeTruthy();
    await act(async () => {
      history.replaceState({ fromList: true }, '', '/chat/10'); // what the browser restores on back
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    expect(screen.queryByRole('dialog', { name: '文章' })).toBeNull();
    expect(screen.getByText('https://telegra.ph/Sample-10-05')).toBeTruthy(); // still in the chat
  });

  it('system back while the media viewer is open (inside the reader) closes only the viewer; the reader back button still works afterwards', async () => {
    history.replaceState({ fromList: true }, '', '/chat/10');
    const api = fakeApi({
      bots: vi.fn(async () => [makeBot({ id: 1 })]),
      chats: vi.fn(async () => [makeChat({ id: 10 })]),
      messages: vi.fn(async () => [linkMsg(summary())]),
      article: vi.fn(async () => makeArticle({ content: everyNode, media: everyMedia })),
    });
    const store = createStore(api, { chatsReloadDelay: 0 });
    render(<App store={store} eventSource={() => new FakeES()} />);
    fireEvent.click(await screen.findByRole('button', { name: /Sample/ }));
    expect(await screen.findByRole('dialog', { name: '文章' })).toBeTruthy();
    expect(history.state).toEqual({ fromList: false, fromChat: true });

    // Open the viewer on one of the article's photos: pushes one more entry for the same URL.
    fireEvent.click(screen.getByRole('button', { name: '查看图片' }));
    await screen.findByRole('dialog', { name: '媒体查看器' });
    expect(history.state).toEqual({ fromList: false, fromChat: true, viewer: true });
    expect(location.pathname).toBe('/chat/10/article/1');

    // System back while the viewer is open: closes only the viewer, not the reader underneath.
    await act(async () => {
      history.replaceState({ fromList: false, fromChat: true }, '', '/chat/10/article/1'); // what the browser restores
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    expect(screen.queryByRole('dialog', { name: '媒体查看器' })).toBeNull();
    expect(screen.getByRole('dialog', { name: '文章' })).toBeTruthy(); // reader still open
    expect(location.pathname).toBe('/chat/10/article/1'); // did not navigate away

    // The reader's own back button still relies on history.state.fromChat, preserved above.
    const back = vi.spyOn(history, 'back').mockImplementation(() => {});
    fireEvent.click(within(screen.getByRole('dialog', { name: '文章' })).getByRole('button', { name: '返回' }));
    expect(back).toHaveBeenCalledTimes(1);
    back.mockRestore();
  });

  it('a same-path popstate that still lands on a viewer entry does not close the viewer (App closes it only on a path change)', async () => {
    history.replaceState({ fromList: true }, '', '/chat/10');
    const api = fakeApi({
      bots: vi.fn(async () => [makeBot({ id: 1 })]),
      chats: vi.fn(async () => [makeChat({ id: 10 })]),
      messages: vi.fn(async () => [linkMsg(summary())]),
      article: vi.fn(async () => makeArticle({ content: everyNode, media: everyMedia })),
    });
    const store = createStore(api, { chatsReloadDelay: 0 });
    render(<App store={store} eventSource={() => new FakeES()} />);
    fireEvent.click(await screen.findByRole('button', { name: /Sample/ }));
    await screen.findByRole('dialog', { name: '文章' });
    fireEvent.click(screen.getByRole('button', { name: '查看图片' }));
    await screen.findByRole('dialog', { name: '媒体查看器' });

    // The router re-parses a fresh route object on every popstate; the path is unchanged here.
    await act(async () => {
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    expect(screen.getByRole('dialog', { name: '媒体查看器' })).toBeTruthy();
  });

  it('deep-links straight into the reader', async () => {
    history.replaceState(null, '', '/chat/10/article/1');
    const api = fakeApi({ chats: vi.fn(async () => [makeChat({ id: 10 })]) });
    const store = createStore(api, { chatsReloadDelay: 0 });
    render(<App store={store} eventSource={() => new FakeES()} />);
    expect(await screen.findByRole('heading', { level: 1, name: 'Sample' })).toBeTruthy();
  });
});

describe('article.scss', () => {
  const css = readFileSync(join(process.cwd(), 'src/components/article/article.scss'), 'utf-8');
  it('covers its whole column (full screen at <=600px, where the middle column is) above the chat header', () => {
    const start = css.indexOf('.ArticleReader {');
    const body = css.slice(start, css.indexOf('}', start));
    expect(body).toMatch(/position:\s*absolute;/);
    expect(body).toMatch(/inset:\s*0;/);
    expect(body).toMatch(/z-index:\s*10;/);
  });
  it('limits the reading column to 732px', () => {
    expect(css).toMatch(/\.ArticleReader-body \{\s*max-width:\s*732px;/);
  });
});
