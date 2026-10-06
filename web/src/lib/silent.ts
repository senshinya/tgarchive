import { signal } from '@preact/signals';

// 静音模式: while on, nothing in the WebUI makes a sound — viewer videos start muted (unmuting one
// lasts for that video only), round video messages, voice and audio messages and article videos
// play muted. Kept per device, like the list mode.
export const SILENT_KEY = 'tgarchive.silent';

function read(): boolean {
  try {
    return localStorage.getItem(SILENT_KEY) === '1';
  } catch {
    return false;
  }
}

export const silent = signal(read());

export function setSilent(on: boolean) {
  silent.value = on;
  try {
    if (on) localStorage.setItem(SILENT_KEY, '1');
    else localStorage.removeItem(SILENT_KEY);
  } catch {
    // Not persisted (private mode, blocked storage): the choice lasts for this page only.
  }
}
