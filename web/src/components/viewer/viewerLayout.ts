// Where slides sit in the viewer, kept apart from Gallery (which loads PhotoSwipe) for testing.

export interface Size {
  x: number;
  y: number;
}

const HEADER_H = 56;
const THUMBS_H = 64;
export const NARROW = 600;
const FALLBACK = { width: 1280, height: 720 };
const NONE = { top: 0, bottom: 0, left: 0, right: 0 };

/** Room PhotoSwipe leaves around a slide. Phones go edge to edge with everything floating over
 * the media, as Telegram does (a video's own controls are lifted above the caption and strip by
 * --viewer-foot). On desktop, photos stay clear of the header and the strip, and videos also of
 * the caption (`foot` is the caption-and-strip block's height), so the player's controls sit on
 * the bottom edge of the picture, as wide as it. */
export function slidePadding(viewport: Size, type: string | undefined, multi: boolean, foot: number) {
  if (viewport.x <= NARROW) return NONE;
  if (type === 'video') return { top: HEADER_H, bottom: Math.max(foot, 8) + 8, left: 64, right: 64 };
  return { top: HEADER_H, bottom: multi ? THUMBS_H + 8 : 16, left: 64, right: 64 };
}

/** A video slide's size: the screen on phones (the picture is letterboxed inside the player and
 * the controls run along the bottom), the video's own size on desktop. */
export function videoSlideSize(viewport: Size, width: number, height: number) {
  if (viewport.x <= NARROW) return { width: viewport.x, height: viewport.y };
  return width > 0 && height > 0 ? { width, height } : FALLBACK;
}

/** Quarter turns (0–3) after rotating once clockwise (1) or counter-clockwise (-1). */
export function turn(quarters: number, dir: 1 | -1) {
  return (quarters + dir + 4) % 4;
}

/** A picture's width and height as shown after `quarters` quarter turns. */
export function turned(width: number, height: number, quarters: number) {
  return quarters % 2 ? { width: height, height: width } : { width, height };
}
