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

/**
 * Subscribes to /api/events. `onResync` runs after every reconnect so the caller can refetch
 * whatever it may have missed. When the browser gives up (readyState CLOSED, e.g. the
 * forward-auth session expired) a new connection is attempted every RETRY_MS. `onDown`, if
 * given, fires once when that happens (not for a transient CONNECTING retry the browser handles
 * on its own) and is armed again once the stream reopens, so it fires once per outage.
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

  const open = () => {
    es = factory('/api/events');
    for (const type of EVENT_TYPES) {
      es.addEventListener(type, (ev) => {
        let data: unknown;
        try {
          data = JSON.parse(ev.data);
        } catch {
          return;
        }
        onEvent({ type, data } as ArchiveEvent);
      });
    }
    es.onopen = () => {
      if (dropped) {
        dropped = false;
        notifiedDown = false;
        onResync();
      }
    };
    es.onerror = () => {
      dropped = true;
      if (es && es.readyState === CLOSED && !stopped) {
        es.close();
        if (!notifiedDown) {
          notifiedDown = true;
          onDown?.();
        }
        timer = setTimeout(open, RETRY_MS);
      }
    };
  };

  open();
  return () => {
    stopped = true;
    clearTimeout(timer);
    es?.close();
  };
}
