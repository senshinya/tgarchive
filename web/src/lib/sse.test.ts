import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ArchiveEvent } from '../api/types';
import { RETRY_MS, connectEvents, type EventSourceLike } from './sse';

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

afterEach(() => {
  FakeES.all = [];
  vi.useRealTimers();
});

describe('connectEvents', () => {
  it('parses named events and ignores malformed data', () => {
    const got: ArchiveEvent[] = [];
    const stop = connectEvents((e) => got.push(e), () => {}, (u) => new FakeES(u));
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
    connectEvents(() => {}, resync, (u) => new FakeES(u));
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
    const stop = connectEvents(() => {}, resync, (u) => new FakeES(u));
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
    const stop = connectEvents(() => {}, () => {}, (u) => new FakeES(u), onDown);
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
    connectEvents(() => {}, () => {}, (u) => new FakeES(u), onDown);
    const es = FakeES.all[0];
    es.readyState = 0;
    es.onerror?.(new Event('error'));
    expect(onDown).not.toHaveBeenCalled();
  });
});
