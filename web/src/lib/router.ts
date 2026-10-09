import { signal } from '@preact/signals';

export type Route =
  | { name: 'home' }
  | { name: 'chat'; chatId: number }
  | { name: 'article'; chatId: number; messageId: number }
  | { name: 'comments'; chatId: number; messageId: number }
  | { name: 'bot'; botId: number }
  | { name: 'bot-article'; botId: number; messageId: number }
  | { name: 'downloads' }
  | { name: 'favorites' }
  | { name: 'media' }
  | { name: 'stats' }
  | { name: 'settings' }
  | { name: 'settings-add-bot' }
  | { name: 'settings-bot'; botId: number }
  | { name: 'settings-telegram-app' }
  | { name: 'settings-userbot' }
  | { name: 'settings-watches' }
  | { name: 'settings-watch-new' }
  | { name: 'settings-watch'; watchId: number };

export function parseRoute(pathname: string): Route {
  const parts = pathname.split('/').filter(Boolean);
  const id = (s: string | undefined) => (s && /^[1-9][0-9]{0,15}$/.test(s) ? Number(s) : 0);
  if (parts[0] === 'chat' && parts.length === 2 && id(parts[1])) return { name: 'chat', chatId: id(parts[1]) };
  if (parts[0] === 'chat' && parts.length === 4 && parts[2] === 'article' && id(parts[1]) && id(parts[3])) {
    return { name: 'article', chatId: id(parts[1]), messageId: id(parts[3]) };
  }
  if (parts[0] === 'chat' && parts.length === 4 && parts[2] === 'comments' && id(parts[1]) && id(parts[3])) {
    return { name: 'comments', chatId: id(parts[1]), messageId: id(parts[3]) };
  }
  if (parts[0] === 'bot' && parts.length === 2 && id(parts[1])) return { name: 'bot', botId: id(parts[1]) };
  if (parts[0] === 'bot' && parts.length === 4 && parts[2] === 'article' && id(parts[1]) && id(parts[3])) {
    return { name: 'bot-article', botId: id(parts[1]), messageId: id(parts[3]) };
  }
  if (parts[0] === 'downloads' && parts.length === 1) return { name: 'downloads' };
  if (parts[0] === 'favorites' && parts.length === 1) return { name: 'favorites' };
  if (parts[0] === 'media' && parts.length === 1) return { name: 'media' };
  if (parts[0] === 'stats' && parts.length === 1) return { name: 'stats' };
  if (parts[0] === 'settings') {
    if (parts.length === 1) return { name: 'settings' };
    if (parts[1] === 'bots' && parts[2] === 'new' && parts.length === 3) return { name: 'settings-add-bot' };
    if (parts[1] === 'bots' && parts.length === 3 && id(parts[2])) return { name: 'settings-bot', botId: id(parts[2]) };
    if (parts[1] === 'telegram-app' && parts.length === 2) return { name: 'settings-telegram-app' };
    if (parts[1] === 'userbot' && parts.length === 2) return { name: 'settings-userbot' };
    if (parts[1] === 'watches' && parts.length === 2) return { name: 'settings-watches' };
    if (parts[1] === 'watches' && parts[2] === 'new' && parts.length === 3) return { name: 'settings-watch-new' };
    if (parts[1] === 'watches' && parts.length === 3 && id(parts[2])) return { name: 'settings-watch', watchId: id(parts[2]) };
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
    case 'comments':
      return `/chat/${r.chatId}/comments/${r.messageId}`;
    case 'bot':
      return `/bot/${r.botId}`;
    case 'bot-article':
      return `/bot/${r.botId}/article/${r.messageId}`;
    case 'downloads':
      return '/downloads';
    case 'favorites':
      return '/favorites';
    case 'media':
      return '/media';
    case 'stats':
      return '/stats';
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
    case 'settings-watches':
      return '/settings/watches';
    case 'settings-watch-new':
      return '/settings/watches/new';
    case 'settings-watch':
      return `/settings/watches/${r.watchId}`;
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

/**
 * The conversation a route shows (the article reader overlays its conversation); 0 for none.
 * Conversation keys are chat ids for one bot × sender chat, and the negated bot id for a bot's
 * merged timeline.
 */
export function routeConvKey(r: Route): number {
  if (r.name === 'chat' || r.name === 'article' || r.name === 'comments') return r.chatId;
  if (r.name === 'bot' || r.name === 'bot-article') return -r.botId;
  return 0;
}

/** The article message a route shows over its conversation; 0 for none. */
export function routeArticleId(r: Route): number {
  return r.name === 'article' || r.name === 'bot-article' ? r.messageId : 0;
}

/** The archived post whose comments a route shows over its conversation; 0 for none. */
export function routeCommentsId(r: Route): number {
  return r.name === 'comments' ? r.messageId : 0;
}

/** The route showing conversation `key` (see routeConvKey). */
export function convRoute(key: number): Route {
  return key < 0 ? { name: 'bot', botId: -key } : { name: 'chat', chatId: key };
}

/** The route showing article `messageId` over conversation `key`. */
export function articleRoute(key: number, messageId: number): Route {
  return key < 0 ? { name: 'bot-article', botId: -key, messageId } : { name: 'article', chatId: key, messageId };
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
