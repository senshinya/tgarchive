import type { VideoProgress, VideoProgressInput } from '../../api/types';
import { PlayerStorage } from '../viewer/playerStorage';

/** Serializes saves; only actual playback can replace the server's resume point. */
export class RemotePlayerStorage extends PlayerStorage {
  private started = false;
  private frozen = false;
  private latest: VideoProgressInput | null = null;
  private pending: VideoProgressInput | null = null;
  private saving: Promise<void> | null = null;
  private lastSave = -Infinity;
  private warned = false;
  private completed = false;
  constructor(
    id: number,
    private readonly getDuration: () => number,
    private readonly initial: VideoProgress,
    private readonly send: (p: VideoProgressInput, keepalive?: boolean) => Promise<VideoProgress>,
    private readonly onError: () => void,
  ) {
    super(id, getDuration);
    this.completed = initial.completed;
  }
  override async getTime() {
    return this.initial.completed ? 0 : this.initial.position;
  }
  start() {
    this.started = true;
  }
  override async setTime(position: number, ended = false) {
    if (this.frozen || !this.started || !Number.isFinite(position)) return;
    const duration = this.getDuration();
    if (!Number.isFinite(duration) || duration <= 0) return;
    const time = Math.max(0, Math.min(duration, position));
    this.completed ||= ended || time / duration >= 0.95;
    this.latest = { position: time, duration, completed: this.completed };
    if (ended || Date.now() - this.lastSave >= 10000) await this.flush();
  }
  async flush() {
    if (!this.latest) return;
    this.pending = { ...this.latest };
    if (this.saving) return this.saving;
    this.lastSave = Date.now();
    this.saving = (async () => {
      while (this.pending) {
        const p = this.pending;
        this.pending = null;
        try {
          await this.send(p, true);
          this.warned = false;
        } catch {
          if (!this.warned) {
            this.onError();
            this.warned = true;
          }
        }
      }
    })();
    try {
      await this.saving;
    } finally {
      this.saving = null;
    }
  }
  snapshot(): VideoProgress {
    return this.latest ? { ...this.latest, updated_at: Math.floor(Date.now() / 1000) } : { ...this.initial };
  }
  override freeze() {
    void this.flush();
    this.frozen = true;
    super.freeze();
  }
}
