import { afterEach, describe, expect, it } from 'vitest';
import {
  articleRoute,
  convRoute,
  isSettings,
  navigate,
  parseRoute,
  route,
  routeArticleId,
  routeConvKey,
  routePath,
  startRouter,
  type Route,
} from './router';

describe('router', () => {
  afterEach(() => history.replaceState(null, '', '/'));

  it('round-trips every route', () => {
    const all: Route[] = [
      { name: 'home' },
      { name: 'chat', chatId: 12 },
      { name: 'article', chatId: 12, messageId: 345 },
      { name: 'bot', botId: 3 },
      { name: 'bot-article', botId: 3, messageId: 345 },
      { name: 'downloads' },
      { name: 'favorites' },
      { name: 'settings' },
      { name: 'settings-add-bot' },
      { name: 'settings-bot', botId: 3 },
      { name: 'settings-telegram-app' },
      { name: 'settings-userbot' },
      { name: 'settings-watches' },
      { name: 'settings-watch-new' },
      { name: 'settings-watch', watchId: 4 },
    ];
    for (const r of all) expect(parseRoute(routePath(r))).toEqual(r);
  });

  it('falls back to home for unknown or malformed paths', () => {
    for (const p of [
      '/nope',
      '/chat',
      '/chat/0',
      '/chat/abc',
      '/chat/1/2',
      '/settings/bots/x',
      '/settings/zzz',
      '/chat/1/article',
      '/chat/1/article/0',
      '/chat/1/articles/2',
      '/chat/1/article/2/3',
      '/bot',
      '/bot/0',
      '/bot/x',
      '/bot/1/2',
      '/bot/1/article',
      '/bot/1/article/0',
      '/downloads/1',
      '/favorites/1',
    ]) {
      expect(parseRoute(p)).toEqual({ name: 'home' });
    }
    expect(parseRoute('/chat/7/')).toEqual({ name: 'chat', chatId: 7 });
  });

  it('flags settings routes', () => {
    expect(isSettings({ name: 'settings-bot', botId: 1 })).toBe(true);
    expect(isSettings({ name: 'chat', chatId: 1 })).toBe(false);
  });

  it('navigates with history and follows popstate', () => {
    const stop = startRouter();
    navigate({ name: 'chat', chatId: 5 });
    expect(location.pathname).toBe('/chat/5');
    expect(route.value).toEqual({ name: 'chat', chatId: 5 });
    history.replaceState(null, '', '/settings');
    window.dispatchEvent(new PopStateEvent('popstate'));
    expect(route.value).toEqual({ name: 'settings' });
    stop();
  });

  it('pushes by default and marks history.state.fromList false when not given', () => {
    navigate({ name: 'chat', chatId: 5 });
    expect(location.pathname).toBe('/chat/5');
    expect(history.state).toEqual({ fromList: false });
  });

  it('marks history.state.fromList true when opened from the chat list, so the header back button can use history.back()', () => {
    navigate({ name: 'chat', chatId: 5 }, { fromList: true });
    expect(history.state).toEqual({ fromList: true });
  });

  it('replaces the current entry instead of pushing when opts.replace is set', () => {
    history.pushState(null, '', '/chat/5');
    const before = history.length;
    navigate({ name: 'settings' }, { replace: true });
    expect(location.pathname).toBe('/settings');
    expect(history.length).toBe(before);
    expect(history.state).toEqual({ fromList: false });
  });

  it('maps routes to the conversation they show: chat id, or the negated bot id for a bot timeline', () => {
    expect(routeConvKey({ name: 'chat', chatId: 4 })).toBe(4);
    expect(routeConvKey({ name: 'article', chatId: 4, messageId: 9 })).toBe(4);
    expect(routeConvKey({ name: 'bot', botId: 2 })).toBe(-2);
    expect(routeConvKey({ name: 'bot-article', botId: 2, messageId: 9 })).toBe(-2);
    expect(routeConvKey({ name: 'settings' })).toBe(0);
    expect(routeConvKey({ name: 'downloads' })).toBe(0);
    expect(routeArticleId({ name: 'article', chatId: 4, messageId: 9 })).toBe(9);
    expect(routeArticleId({ name: 'bot-article', botId: 2, messageId: 8 })).toBe(8);
    expect(routeArticleId({ name: 'chat', chatId: 4 })).toBe(0);
  });

  it('builds conversation and article routes from a conversation key', () => {
    expect(convRoute(4)).toEqual({ name: 'chat', chatId: 4 });
    expect(convRoute(-2)).toEqual({ name: 'bot', botId: 2 });
    expect(articleRoute(4, 9)).toEqual({ name: 'article', chatId: 4, messageId: 9 });
    expect(articleRoute(-2, 9)).toEqual({ name: 'bot-article', botId: 2, messageId: 9 });
  });

  it('marks an article opened from its chat with fromChat', () => {
    navigate({ name: 'article', chatId: 5, messageId: 6 }, { fromChat: true });
    expect(location.pathname).toBe('/chat/5/article/6');
    expect(history.state).toEqual({ fromList: false, fromChat: true });
  });

  it('strips a stale history.state.viewer marker on start (e.g. a reload while the media viewer was open left it on the current entry)', () => {
    history.replaceState({ fromChat: true, viewer: 'token-from-before-reload' }, '', '/chat/5/article/6');
    const stop = startRouter();
    expect(history.state).toEqual({ fromChat: true });
    expect(location.pathname).toBe('/chat/5/article/6'); // only the marker is stripped, nothing navigates
    stop();
  });

  it('leaves history.state alone on start when there is no viewer marker', () => {
    history.replaceState({ fromChat: true }, '', '/chat/5/article/6');
    const stop = startRouter();
    expect(history.state).toEqual({ fromChat: true });
    stop();
  });
});
