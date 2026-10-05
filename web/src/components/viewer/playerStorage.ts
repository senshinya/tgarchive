// What the player remembers (spec §3.5): volume / muted / speed shared by every video, and a
// resume position per video. Implements Vidstack's MediaStorage; every localStorage access is
// guarded so a private window or full quota just means "nothing remembered".

export const PREFS_KEY = 'tgarchive.player';
export const POSITIONS_KEY = 'tgarchive.player.pos';
export const RESUME_MIN = 10; // seconds watched before a position is worth keeping
export const RESUME_TAIL = 10; // a position this close to the end counts as finished
export const MAX_POSITIONS = 200;
const SAVE_EVERY_MS = 2000;

interface Prefs {
  volume?: number;
  muted?: boolean;
  rate?: number;
}
type Positions = Record<string, { t: number; at: number }>;

function read<T extends object>(key: string): T {
  try {
    const v = JSON.parse(localStorage.getItem(key) ?? '{}');
    return v && typeof v === 'object' && !Array.isArray(v) ? (v as T) : ({} as T);
  } catch {
    return {} as T;
  }
}

function write(key: string, v: object) {
  try {
    localStorage.setItem(key, JSON.stringify(v));
  } catch {
    // ignore: remembering is best-effort
  }
}

function num(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) ? v : null;
}

export class PlayerStorage {
  /** While true (a long-press 2× is in effect) speed changes are not remembered. */
  holdRate = false;
  #lastSave = 0;
  #frozen = false;

  constructor(
    private readonly mediaId: number,
    private readonly duration: () => number,
    private readonly now: () => number = Date.now,
  ) {}

  async getVolume() {
    const v = num(read<Prefs>(PREFS_KEY).volume);
    return v !== null && v >= 0 && v <= 1 ? v : null;
  }
  async setVolume(volume: number) {
    if (!this.#frozen) this.#prefs({ volume });
  }
  async getMuted() {
    const v = read<Prefs>(PREFS_KEY).muted;
    return typeof v === 'boolean' ? v : null;
  }
  async setMuted(muted: boolean) {
    if (!this.#frozen) this.#prefs({ muted });
  }
  async getPlaybackRate() {
    const v = num(read<Prefs>(PREFS_KEY).rate);
    return v !== null && v > 0 && v <= 4 ? v : null;
  }
  async setPlaybackRate(rate: number) {
    if (!this.holdRate && !this.#frozen) this.#prefs({ rate });
  }

  async getTime() {
    const p = read<Positions>(POSITIONS_KEY)[this.mediaId];
    const t = num(p?.t);
    return t !== null && t > 0 ? t : null;
  }

  /** Ignore everything from now on (the player is being torn down). */
  freeze() {
    this.#frozen = true;
  }

  async setTime(time: number, ended?: boolean) {
    if (this.#frozen) return;
    const d = this.duration();
    const keep = !ended && time >= RESUME_MIN && !(d > 0 && d - time <= RESUME_TAIL);
    const now = this.now();
    if (keep && now - this.#lastSave < SAVE_EVERY_MS) return;
    this.#lastSave = now;
    const all = read<Positions>(POSITIONS_KEY);
    if (!keep) {
      if (!(this.mediaId in all)) return;
      delete all[this.mediaId];
    } else {
      all[this.mediaId] = { t: Math.floor(time), at: now };
      const ids = Object.keys(all);
      if (ids.length > MAX_POSITIONS) {
        ids.sort((a, b) => (all[a]?.at ?? 0) - (all[b]?.at ?? 0));
        for (const id of ids.slice(0, ids.length - MAX_POSITIONS)) delete all[id];
      }
    }
    write(POSITIONS_KEY, all);
  }

  // Not used by this app; Vidstack asks for them on load.
  async getLang() {
    return null;
  }
  async getCaptions() {
    return null;
  }
  async getVideoQuality() {
    return null;
  }
  async getAudioGain() {
    return null;
  }

  #prefs(patch: Prefs) {
    write(PREFS_KEY, { ...read<Prefs>(PREFS_KEY), ...patch });
  }
}
