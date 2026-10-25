import { describe, expect, it, vi } from 'vitest';
import { RemotePlayerStorage } from './progress';
import type { VideoProgressInput } from '../../api/types';
const initial = {
  position: 41,
  duration: 120,
  completed: false,
  updated_at: 1,
};
describe('server playback storage', () => {
  it('restores server position and ignores startup zero before playback', async () => {
    const send = vi.fn(async (_p: VideoProgressInput, _keepalive?: boolean) => initial);
    const storage = new RemotePlayerStorage(1, () => 120, initial, send, vi.fn());
    expect(await storage.getTime()).toBe(41);
    await storage.setTime(0);
    storage.freeze();
    expect(send).not.toHaveBeenCalled();
  });
  it('flushes the latest position on pause and retains completion when replaying', async () => {
    const send = vi.fn(async (_p: VideoProgressInput, _keepalive?: boolean) => initial);
    const storage = new RemotePlayerStorage(1, () => 120, { ...initial, completed: true }, send, vi.fn());
    expect(await storage.getTime()).toBe(0);
    storage.start();
    await storage.setTime(17);
    await storage.flush();
    expect(send).toHaveBeenLastCalledWith({ position: 17, duration: 120, completed: true }, true);
    storage.freeze();
    await storage.setTime(0);
    expect(send).not.toHaveBeenCalledWith({ position: 0, duration: 120, completed: false }, expect.anything());
  });
  it('serializes overlapping writes so an older response cannot regress the final save', async () => {
    let finish: (v: typeof initial) => void = () => {};
    const send = vi
      .fn()
      .mockImplementationOnce(
        () =>
          new Promise((r) => {
            finish = r;
          }),
      )
      .mockResolvedValue(initial);
    const storage = new RemotePlayerStorage(1, () => 120, initial, send, vi.fn());
    storage.start();
    void storage.setTime(50);
    await Promise.resolve();
    void storage.setTime(75);
    const flush = storage.flush();
    finish(initial);
    await flush;
    expect(send.mock.calls.at(-1)?.[0].position).toBe(75);
  });
});

it('keeps a video watched after finishing and replaying in the same session', async () => {
  const send = vi.fn(async (_p: VideoProgressInput, _keepalive?: boolean) => initial);
  const storage = new RemotePlayerStorage(1, () => 120, initial, send, vi.fn());
  storage.start();
  await storage.setTime(120, true);
  await storage.setTime(3);
  await storage.flush();
  expect(send.mock.calls.at(-1)?.[0]).toEqual({
    position: 3,
    duration: 120,
    completed: true,
  });
});
