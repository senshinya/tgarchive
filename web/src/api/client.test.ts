import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, NETWORK_ERROR, api, avatarUrl, errorMessage, mediaUrl } from './client';

type Call = { url: string; init: RequestInit };

function mockFetch(status: number, body?: unknown): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit) => {
      calls.push({ url, init });
      const text = body === undefined ? '' : typeof body === 'string' ? body : JSON.stringify(body);
      return new Response(status === 204 ? null : text, { status });
    }),
  );
  return calls;
}

afterEach(() => vi.unstubAllGlobals());

describe('api client', () => {
  it('builds read URLs with cursor and limit', async () => {
    const calls = mockFetch(200, []);
    await api.messages(7);
    await api.messages(7, 120, 50);
    await api.chatMedia(7, 'file', 33);
    await api.chats();
    await api.article(55);
    expect(calls.map((c) => c.url)).toEqual([
      '/api/chats/7/messages?limit=50',
      '/api/chats/7/messages?before=120&limit=50',
      '/api/chats/7/media?type=file&before=33&limit=50',
      '/api/chats',
      '/api/messages/55/article',
    ]);
    expect(calls[0].init.method).toBe('GET');
    expect(calls[0].init.body).toBeUndefined();
  });

  it('pages conversations after or around a message', async () => {
    const calls = mockFetch(200, []);
    await api.messages(7, 0, 50, { around: 90 });
    await api.botMessages(2, 0, 50, { after: 12 });
    expect(calls.map((c) => c.url)).toEqual(['/api/chats/7/messages?limit=50&around=90', '/api/bots/2/messages?limit=50&after=12']);
  });

  it('sends JSON bodies with the JSON content type the CSRF guard requires', async () => {
    const calls = mockFetch(204);
    await api.putWhitelist(3, 42, '朋友', true);
    expect(calls[0].url).toBe('/api/admin/bots/3/whitelist/42');
    expect(calls[0].init.method).toBe('PUT');
    expect((calls[0].init.headers as Record<string, string>)['Content-Type']).toBe('application/json');
    expect(JSON.parse(String(calls[0].init.body))).toEqual({ note: '朋友', can_fetch: true });
  });

  it('sends every write as JSON, {} when it has no body', async () => {
    const calls = mockFetch(204);
    await expect(api.retryMedia(9)).resolves.toBeUndefined();
    await api.userbotLogout();
    await api.deleteMessage(4);
    await api.unfavorite(4);
    await api.favorite(4);
    expect(calls.map((c) => `${c.init.method} ${c.url}`)).toEqual([
      'POST /api/media/9/retry',
      'POST /api/admin/userbot/logout',
      'DELETE /api/messages/4',
      'DELETE /api/messages/4/favorite',
      'PUT /api/messages/4/favorite',
    ]);
    for (const c of calls) {
      expect((c.init.headers as Record<string, string>)['Content-Type']).toBe('application/json');
      expect(c.init.body).toBe('{}');
    }
  });

  it('sends reads without a body or content type', async () => {
    const calls = mockFetch(200, []);
    await api.bots();
    expect(calls[0].init.headers).toBeUndefined();
    expect(calls[0].init.body).toBeUndefined();
  });

  it('reads the channel list with GET and asks for a rescan with POST', async () => {
    const calls = mockFetch(200, { channels: [], loading: false, updated_at: 0, error: '' });
    await api.channels();
    await api.channels(true);
    expect(calls.map((c) => `${c.init.method} ${c.url}`)).toEqual(['GET /api/admin/channels', 'POST /api/admin/channels/refresh']);
  });

  it('adds purge only when asked', async () => {
    const calls = mockFetch(204);
    await api.deleteBot(5, false);
    await api.deleteBot(5, true);
    expect(calls.map((c) => c.url)).toEqual(['/api/admin/bots/5', '/api/admin/bots/5?purge=1']);
    expect(calls[0].init.method).toBe('DELETE');
  });

  it('turns error JSON into ApiError carrying status, message and body', async () => {
    mockFetch(409, { error: '机器人已存在', bot_id: 4, steps: [{ step: 'getMe', ok: true, detail: '@x' }] });
    const err = await api.addBot('1:abc').catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(409);
    expect(err.message).toBe('机器人已存在');
    expect(err.body.steps).toHaveLength(1);
  });

  it('maps 401 and network failures to the re-login hint', async () => {
    mockFetch(401, { error: 'unauthorized' });
    expect((await api.bots().catch((e) => e)).message).toBe(NETWORK_ERROR);
    vi.stubGlobal('fetch', vi.fn(async () => Promise.reject(new TypeError('Failed to fetch'))));
    const err = await api.bots().catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(0);
    expect(errorMessage(err)).toBe(NETWORK_ERROR);
  });

  it('reports non-JSON error bodies by status', async () => {
    mockFetch(405, 'Method Not Allowed');
    expect((await api.bots().catch((e) => e)).message).toBe('HTTP 405');
  });

  it('builds media and avatar URLs', () => {
    expect(mediaUrl(12)).toBe('/media/12');
    expect(mediaUrl(12, true)).toBe('/media/12?download=1');
    expect(avatarUrl('senders', 42)).toBe('/avatars/senders/42');
  });
});
