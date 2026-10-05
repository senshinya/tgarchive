import { EVENT_TYPES, type ArchiveEvent } from '../api/types';

export interface EventSourceLike {
  readyState: number;
  onopen: ((ev: Event) => unknown) | null;
  onerror: ((ev: Event) => unknown) | null;
  addEventListener(type: string, listener: (ev: MessageEvent) => void): void;
  close(): void;
}

export type EventSourceFactory = (url: string) => EventSourceLike;

const CLOSED = 2;
export const RETRY_MS = 5000;
/** How often the watchdog checks the stream while the page is visible. */
export const WATCHDOG_MS = 15_000;
/** The server pings every 25s; a visible page that heard nothing for this long has a dead stream. */
export const STALE_MS = 60_000;
/** Coming back to the page after this long away forces a reconnect and resync. */
export const AWAY_MS = 30_000;

/**
 * Subscribes to /api/events. `onResync` runs after every reconnect so the caller can refetch
 * whatever it may have missed. When the browser gives up (readyState CLOSED, e.g. the
 * forward-auth session expired) a new connection is attempted every RETRY_MS. `onDown`, if
 * given, fires once when that happens (not for a transient CONNECTING retry the browser handles
 * on its own) and is armed again once the stream reopens, so it fires once per outage.
 *
 * A backgrounded mobile tab is often frozen without the stream ever reporting an error: it looks
 * OPEN but is dead, and everything sent meanwhile is lost. So the stream is also replaced (and
 * the caller resyncs) when a visible page has heard nothing — not even the server's ping — for
 * STALE_MS, and when the page comes back after more than AWAY_MS hidden, is restored from the
 * back/forward cache, or the network comes back online.
 */
export function connectEvents(
  onEvent: (ev: ArchiveEvent) => void,
  onResync: () => void,
  factory: EventSourceFactory = (url) => new EventSource(url),
  onDown?: () => void,
): () => void {
  let es: EventSourceLike | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let stopped = false;
  let dropped = false;
  let notifiedDown = false;
  let lastSeen = Date.now();
  let hiddenAt = 0;

  const open = () => {
    const self = factory('/api/events');
    es = self;
    lastSeen = Date.now();
    const live = () => es === self && !stopped;
    for (const type of EVENT_TYPES) {
      self.addEventListener(type, (ev) => {
        if (!live()) return;
        lastSeen = Date.now();
        let data: unknown;
        try {
          data = JSON.parse(ev.data);
        } catch {
          return;
        }
        onEvent({ type, data } as ArchiveEvent);
      });
    }
    self.addEventListener('ping', () => {
      if (live()) lastSeen = Date.now();
    });
    self.onopen = () => {
      if (!live()) return;
      lastSeen = Date.now();
      if (dropped) {
        dropped = false;
        notifiedDown = false;
        onResync();
      }
    };
    self.onerror = () => {
      if (!live()) return;
      dropped = true;
      if (self.readyState === CLOSED) {
        self.close();
        if (!notifiedDown) {
          notifiedDown = true;
          onDown?.();
        }
        timer = setTimeout(open, RETRY_MS);
      }
    };
  };

  let lastReconnect = 0;
  /** Replaces the stream now; its onopen then resyncs. A bfcache restore fires both pageshow
   * and visibilitychange, so a second request right after the first is dropped. */
  const reconnect = () => {
    if (stopped || Date.now() - lastReconnect < 2000) return;
    lastReconnect = Date.now();
    clearTimeout(timer);
    es?.close();
    dropped = true;
    open();
  };

  const hidden = () => typeof document !== 'undefined' && document.visibilityState === 'hidden';

  const watchdog = setInterval(() => {
    if (!hidden() && Date.now() - lastSeen > STALE_MS) reconnect();
  }, WATCHDOG_MS);

  const onVisibility = () => {
    if (hidden()) {
      hiddenAt = Date.now();
      return;
    }
    const away = hiddenAt ? Date.now() - hiddenAt : 0;
    hiddenAt = 0;
    if (away > AWAY_MS || Date.now() - lastSeen > STALE_MS) reconnect();
  };
  const onPageShow = (ev: Event) => {
    if ((ev as PageTransitionEvent).persisted) reconnect();
  };
  const onOnline = () => reconnect();

  if (typeof document !== 'undefined') document.addEventListener('visibilitychange', onVisibility);
  if (typeof window !== 'undefined') {
    window.addEventListener('pageshow', onPageShow);
    window.addEventListener('online', onOnline);
  }

  open();
  return () => {
    stopped = true;
    clearTimeout(timer);
    clearInterval(watchdog);
    es?.close();
    if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', onVisibility);
    if (typeof window !== 'undefined') {
      window.removeEventListener('pageshow', onPageShow);
      window.removeEventListener('online', onOnline);
    }
  };
}
