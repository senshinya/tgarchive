import { signal } from '@preact/signals';

export type Route =
  | { name: 'home' }
  | { name: 'chat'; chatId: number }
  | { name: 'article'; chatId: number; messageId: number }
  | { name: 'settings' }
  | { name: 'settings-add-bot' }
  | { name: 'settings-bot'; botId: number }
  | { name: 'settings-telegram-app' }
  | { name: 'settings-userbot' };

export function parseRoute(pathname: string): Route {
  const parts = pathname.split('/').filter(Boolean);
  const id = (s: string | undefined) => (s && /^[1-9][0-9]{0,15}$/.test(s) ? Number(s) : 0);
  if (parts[0] === 'chat' && parts.length === 2 && id(parts[1])) return { name: 'chat', chatId: id(parts[1]) };
  if (parts[0] === 'chat' && parts.length === 4 && parts[2] === 'article' && id(parts[1]) && id(parts[3])) {
    return { name: 'article', chatId: id(parts[1]), messageId: id(parts[3]) };
  }
  if (parts[0] === 'settings') {
    if (parts.length === 1) return { name: 'settings' };
    if (parts[1] === 'bots' && parts[2] === 'new' && parts.length === 3) return { name: 'settings-add-bot' };
    if (parts[1] === 'bots' && parts.length === 3 && id(parts[2])) return { name: 'settings-bot', botId: id(parts[2]) };
    if (parts[1] === 'telegram-app' && parts.length === 2) return { name: 'settings-telegram-app' };
    if (parts[1] === 'userbot' && parts.length === 2) return { name: 'settings-userbot' };
  }
  return { name: 'home' };
}

export function routePath(r: Route): string {
  switch (r.name) {
    case 'home':
      return '/';
    case 'chat':
      return `/chat/${r.chatId}`;
    case 'article':
      return `/chat/${r.chatId}/article/${r.messageId}`;
    case 'settings':
      return '/settings';
    case 'settings-add-bot':
      return '/settings/bots/new';
    case 'settings-bot':
      return `/settings/bots/${r.botId}`;
    case 'settings-telegram-app':
      return '/settings/telegram-app';
    case 'settings-userbot':
      return '/settings/userbot';
  }
}

export function isSettings(r: Route): boolean {
  return r.name.startsWith('settings');
}

export const route = signal<Route>(parseRoute(typeof location === 'undefined' ? '/' : location.pathname));

export interface NavigateOptions {
  /** Replaces the current history entry instead of pushing a new one. */
  replace?: boolean;
  /** Marks this entry as reached from the chat list, so the chat header's back button can use
   * `history.back()` instead of pushing a fresh "/" entry (keeps Android's hardware back in sync
   * with the in-app back button instead of requiring an extra press to leave the app). */
  fromList?: boolean;
  /** Marks an article reader opened from its chat, so closing it can use `history.back()` (the
   * system back button and the reader's own back button then do the same thing). */
  fromChat?: boolean;
}

/** The chat a route shows (the article reader overlays its chat); 0 for none. */
export function routeChatId(r: Route): number {
  return r.name === 'chat' || r.name === 'article' ? r.chatId : 0;
}

export function navigate(to: Route, opts: NavigateOptions = {}): void {
  const path = routePath(to);
  const state: { fromList: boolean; fromChat?: boolean } = { fromList: !!opts.fromList };
  if (opts.fromChat) state.fromChat = true;
  if (opts.replace) history.replaceState(state, '', path);
  else history.pushState(state, '', path);
  route.value = to;
}

/** Drops a stale `viewer` marker from the current history entry, if one is there. The media
 * viewer (MediaViewer.tsx) pushes `{ ..., viewer: <token> }` on open and pops it on close, but
 * history.state survives a reload: reloading while the viewer was open lands back on an entry
 * that still carries that marker even though nothing in the freshly started app has it open.
 * Left in place, that marker could make a later open's first Back/X press land on it instead of
 * closing cleanly. Called once at app start. */
function stripStaleViewerMarker(): void {
  const state = history.state as Record<string, unknown> | null;
  if (!state || !('viewer' in state)) return;
  const { viewer: _viewer, ...rest } = state;
  history.replaceState(rest, '', location.href);
}

/** Keeps `route` in sync with browser back/forward. Returns an unsubscribe function. */
export function startRouter(): () => void {
  stripStaleViewerMarker();
  const onPop = () => {
    route.value = parseRoute(location.pathname);
  };
  window.addEventListener('popstate', onPop);
  onPop();
  return () => window.removeEventListener('popstate', onPop);
}
