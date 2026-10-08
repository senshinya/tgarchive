// Where slides sit in the viewer, kept apart from Gallery (which loads PhotoSwipe) for testing.

export interface Size {
  x: number;
  y: number;
}

const HEADER_H = 56;
const THUMBS_H = 64;
export const NARROW = 600;
const NONE = { top: 0, bottom: 0, left: 0, right: 0 };

/** Room PhotoSwipe leaves around a slide. Desktop photos stay clear of the header and the
 * thumbnail strip; phone photos go edge to edge with the controls floating over them, as Telegram
 * does. Videos fill the screen everywhere: the caption and strip stay at the bottom, as for
 * photos, and the player's own controls are lifted above them (--viewer-foot). */
export function slidePadding(viewport: Size, type: string | undefined, multi: boolean) {
  if (type === 'video' || viewport.x <= NARROW) return NONE;
  return { top: HEADER_H, bottom: multi ? THUMBS_H + 8 : 16, left: 64, right: 64 };
}

/** A video slide is the size of the viewport: the picture is letterboxed inside the player
 * (object-fit), and the controls run along the bottom of the screen. */
export function videoSlideSize(viewport: Size) {
  return { width: viewport.x, height: viewport.y };
}
