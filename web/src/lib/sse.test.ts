import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ArchiveEvent } from '../api/types';
import { AWAY_MS, RETRY_MS, STALE_MS, WATCHDOG_MS, connectEvents, type EventSourceLike } from './sse';

class FakeES implements EventSourceLike {
  static all: FakeES[] = [];
  readyState = 0;
  onopen: ((ev: Event) => unknown) | null = null;
  onerror: ((ev: Event) => unknown) | null = null;
  listeners = new Map<string, (ev: MessageEvent) => void>();
  closed = false;
  constructor(public url: string) {
    FakeES.all.push(this);
  }
  addEventListener(type: string, l: (ev: MessageEvent) => void) {
    this.listeners.set(type, l);
  }
  close() {
    this.closed = true;
  }
  emit(type: string, data: string) {
    this.listeners.get(type)?.(new MessageEvent(type, { data }));
  }
}

// Every connection registers document/window listeners; stop them all between tests so a
// leftover connection from an earlier test cannot react to a later test's events.
const stops: (() => void)[] = [];
const connect = (...args: Parameters<typeof connectEvents>) => {
  const stop = connectEvents(...args);
  stops.push(stop);
  return stop;
};

function setVisibility(state: 'visible' | 'hidden') {
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => state });
  document.dispatchEvent(new Event('visibilitychange'));
}

afterEach(() => {
  for (const s of stops.splice(0)) s();
  setVisibility('visible');
  FakeES.all = [];
  vi.useRealTimers();
});

describe('connectEvents', () => {
  it('parses named events and ignores malformed data', () => {
    const got: ArchiveEvent[] = [];
    const stop = connect((e) => got.push(e), () => {}, (u) => new FakeES(u));
    const es = FakeES.all[0];
    expect(es.url).toBe('/api/events');
    es.emit('message.created', '{"chat_id":1,"message_id":2}');
    es.emit('bot.status', 'not json');
    es.emit('media.updated', '{"media_id":3,"message_ids":null}');
    expect(got).toEqual([
      { type: 'message.created', data: { chat_id: 1, message_id: 2 } },
      { type: 'media.updated', data: { media_id: 3, message_ids: null } },
    ]);
    stop();
    expect(es.closed).toBe(true);
  });

  it('resyncs after the browser reconnects on its own', () => {
    const resync = vi.fn();
    connect(() => {}, resync, (u) => new FakeES(u));
    const es = FakeES.all[0];
    es.onopen?.(new Event('open'));
    expect(resync).not.toHaveBeenCalled();
    es.readyState = 0; // CONNECTING: browser retries itself
    es.onerror?.(new Event('error'));
    es.onopen?.(new Event('open'));
    expect(resync).toHaveBeenCalledTimes(1);
    expect(FakeES.all).toHaveLength(1);
  });

  it('opens a new connection when the old one is closed for good', () => {
    vi.useFakeTimers();
    const resync = vi.fn();
    const stop = connect(() => {}, resync, (u) => new FakeES(u));
    const first = FakeES.all[0];
    first.readyState = 2;
    first.onerror?.(new Event('error'));
    expect(first.closed).toBe(true);
    vi.advanceTimersByTime(RETRY_MS);
    expect(FakeES.all).toHaveLength(2);
    FakeES.all[1].onopen?.(new Event('open'));
    expect(resync).toHaveBeenCalledTimes(1);
    stop();
    expect(FakeES.all[1].closed).toBe(true);
  });

  it('calls onDown once per outage, not once per failed retry, and rearms once it reopens', () => {
    vi.useFakeTimers();
    const onDown = vi.fn();
    const stop = connect(() => {}, () => {}, (u) => new FakeES(u), onDown);
    FakeES.all[0].readyState = 2;
    FakeES.all[0].onerror?.(new Event('error'));
    expect(onDown).toHaveBeenCalledTimes(1);

    // The retry itself also fails to connect: still the same outage, must not notify again.
    vi.advanceTimersByTime(RETRY_MS);
    FakeES.all[1].readyState = 2;
    FakeES.all[1].onerror?.(new Event('error'));
    expect(onDown).toHaveBeenCalledTimes(1);

    // It recovers, then drops again later: that is a new outage.
    FakeES.all[1].onopen?.(new Event('open'));
    FakeES.all[1].readyState = 2;
    FakeES.all[1].onerror?.(new Event('error'));
    expect(onDown).toHaveBeenCalledTimes(2);
    stop();
  });

  it('does not call onDown for a transient CONNECTING error the browser retries itself', () => {
    const onDown = vi.fn();
    connect(() => {}, () => {}, (u) => new FakeES(u), onDown);
    const es = FakeES.all[0];
    es.readyState = 0;
    es.onerror?.(new Event('error'));
    expect(onDown).not.toHaveBeenCalled();
  });

  it('treats ping events as liveness and ignores them otherwise', () => {
    vi.useFakeTimers();
    const got: ArchiveEvent[] = [];
    const resync = vi.fn();
    connect((e) => got.push(e), resync, (u) => new FakeES(u));
    const es = FakeES.all[0];
    es.onopen?.(new Event('open'));
    for (let t = 0; t < STALE_MS * 2; t += 20_000) {
      vi.advanceTimersByTime(20_000);
      es.emit('ping', '{}');
    }
    expect(got).toEqual([]);
    expect(FakeES.all).toHaveLength(1);
    expect(resync).not.toHaveBeenCalled();
  });

  it('replaces a silently dead stream on a visible page and resyncs', () => {
    vi.useFakeTimers();
    const resync = vi.fn();
    const got: ArchiveEvent[] = [];
    connect((e) => got.push(e), resync, (u) => new FakeES(u));
    FakeES.all[0].onopen?.(new Event('open'));
    vi.advanceTimersByTime(STALE_MS + WATCHDOG_MS);
    expect(FakeES.all).toHaveLength(2);
    expect(FakeES.all[0].closed).toBe(true);
    FakeES.all[1].onopen?.(new Event('open'));
    expect(resync).toHaveBeenCalledTimes(1);
    // Events from the replaced stream are ignored; the new one delivers.
    FakeES.all[0].emit('message.created', '{"chat_id":1,"message_id":1}');
    FakeES.all[1].emit('message.created', '{"chat_id":1,"message_id":2}');
    expect(got).toEqual([{ type: 'message.created', data: { chat_id: 1, message_id: 2 } }]);
  });

  it('does not run the watchdog while hidden', () => {
    vi.useFakeTimers();
    connect(() => {}, () => {}, (u) => new FakeES(u));
    setVisibility('hidden');
    vi.advanceTimersByTime(STALE_MS * 3);
    expect(FakeES.all).toHaveLength(1);
  });

  it('reconnects and resyncs when the page comes back after a long absence', () => {
    vi.useFakeTimers();
    const resync = vi.fn();
    connect(() => {}, resync, (u) => new FakeES(u));
    FakeES.all[0].onopen?.(new Event('open'));
    setVisibility('hidden');
    vi.advanceTimersByTime(AWAY_MS + 1000);
    setVisibility('visible');
    expect(FakeES.all).toHaveLength(2);
    FakeES.all[1].onopen?.(new Event('open'));
    expect(resync).toHaveBeenCalledTimes(1);
  });

  it('keeps the stream after a short absence', () => {
    vi.useFakeTimers();
    connect(() => {}, () => {}, (u) => new FakeES(u));
    FakeES.all[0].onopen?.(new Event('open'));
    setVisibility('hidden');
    vi.advanceTimersByTime(5000);
    setVisibility('visible');
    expect(FakeES.all).toHaveLength(1);
  });

  it('reconnects on a back/forward cache restore and when the network returns', () => {
    vi.useFakeTimers();
    connect(() => {}, () => {}, (u) => new FakeES(u));
    window.dispatchEvent(Object.assign(new Event('pageshow'), { persisted: false }));
    expect(FakeES.all).toHaveLength(1);
    window.dispatchEvent(Object.assign(new Event('pageshow'), { persisted: true }));
    expect(FakeES.all).toHaveLength(2);
    vi.advanceTimersByTime(3000);
    window.dispatchEvent(new Event('online'));
    expect(FakeES.all).toHaveLength(3);
  });

  it('reconnects once when a restore fires pageshow and visibilitychange together', () => {
    vi.useFakeTimers();
    connect(() => {}, () => {}, (u) => new FakeES(u));
    setVisibility('hidden');
    vi.advanceTimersByTime(AWAY_MS + 1000);
    window.dispatchEvent(Object.assign(new Event('pageshow'), { persisted: true }));
    setVisibility('visible');
    expect(FakeES.all).toHaveLength(2);
  });

  it('stops listening to the page once stopped', () => {
    const stop = connect(() => {}, () => {}, (u) => new FakeES(u));
    stop();
    window.dispatchEvent(new Event('online'));
    expect(FakeES.all).toHaveLength(1);
  });
});
