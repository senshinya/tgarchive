// Which viewer action a key press means (spec §3.4). On a video, plain arrows, space, J/K/L,
// M, F, < > and the volume keys belong to Vidstack's own shortcuts, so they map to nothing here.

export type ViewerKeyAction = 'prev' | 'next' | 'close' | 'zoomIn' | 'zoomOut' | null;

export interface KeyLike {
  key: string;
  shiftKey: boolean;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
  target: EventTarget | null;
}

function ownsKeys(t: EventTarget | null): boolean {
  const el = t as HTMLElement | null;
  if (!el || typeof el.closest !== 'function') return false;
  // A player settings menu handles its own keys (Escape closes just the menu).
  return !!el.closest('input, textarea, select, [contenteditable=""], [contenteditable="true"], [role="menu"], media-menu');
}

export function viewerKeyAction(e: KeyLike, kind: string): ViewerKeyAction {
  if (e.ctrlKey || e.metaKey || e.altKey || ownsKeys(e.target)) return null;
  const video = kind === 'video';
  switch (e.key) {
    case 'Escape':
      return 'close';
    case 'PageUp':
      return 'prev';
    case 'PageDown':
      return 'next';
    case 'ArrowLeft':
      return e.shiftKey || !video ? 'prev' : null;
    case 'ArrowRight':
      return e.shiftKey || !video ? 'next' : null;
    case '+':
    case '=':
      return kind === 'photo' ? 'zoomIn' : null;
    case '-':
    case '_':
      return kind === 'photo' ? 'zoomOut' : null;
    default:
      return null;
  }
}
