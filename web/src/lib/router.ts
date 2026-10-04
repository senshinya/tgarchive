import { signal } from '@preact/signals';

export type Route =
  | { name: 'home' }
  | { name: 'chat'; chatId: number }
  | { name: 'settings' }
  | { name: 'settings-add-bot' }
  | { name: 'settings-bot'; botId: number }
  | { name: 'settings-telegram-app' }
  | { name: 'settings-userbot' };

export function parseRoute(pathname: string): Route {
  const parts = pathname.split('/').filter(Boolean);
  const id = (s: string | undefined) => (s && /^[1-9][0-9]{0,15}$/.test(s) ? Number(s) : 0);
  if (parts[0] === 'chat' && parts.length === 2 && id(parts[1])) return { name: 'chat', chatId: id(parts[1]) };
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

export function navigate(to: Route, replace = false): void {
  const path = routePath(to);
  if (replace) history.replaceState(null, '', path);
  else history.pushState(null, '', path);
  route.value = to;
}

/** Keeps `route` in sync with browser back/forward. Returns an unsubscribe function. */
export function startRouter(): () => void {
  const onPop = () => {
    route.value = parseRoute(location.pathname);
  };
  window.addEventListener('popstate', onPop);
  onPop();
  return () => window.removeEventListener('popstate', onPop);
}
