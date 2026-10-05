import { afterEach, describe, expect, it } from 'vitest';
import { isSettings, navigate, parseRoute, route, routeChatId, routePath, startRouter, type Route } from './router';

describe('router', () => {
  afterEach(() => history.replaceState(null, '', '/'));

  it('round-trips every route', () => {
    const all: Route[] = [
      { name: 'home' },
      { name: 'chat', chatId: 12 },
      { name: 'article', chatId: 12, messageId: 345 },
      { name: 'settings' },
      { name: 'settings-add-bot' },
      { name: 'settings-bot', botId: 3 },
      { name: 'settings-telegram-app' },
      { name: 'settings-userbot' },
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

  it('maps routes to the chat they show', () => {
    expect(routeChatId({ name: 'chat', chatId: 4 })).toBe(4);
    expect(routeChatId({ name: 'article', chatId: 4, messageId: 9 })).toBe(4);
    expect(routeChatId({ name: 'settings' })).toBe(0);
  });

  it('marks an article opened from its chat with fromChat', () => {
    navigate({ name: 'article', chatId: 5, messageId: 6 }, { fromChat: true });
    expect(location.pathname).toBe('/chat/5/article/6');
    expect(history.state).toEqual({ fromList: false, fromChat: true });
  });
});
