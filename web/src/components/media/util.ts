import type { Media, Message } from '../../api/types';

export function mainMedia(msg: Message): Media | undefined {
  return msg.media.find((m) => m.role === 'main');
}

/** The thumbnail, only when it has been downloaded. */
export function readyThumb(msg: Message): Media | undefined {
  return msg.media.find((m) => m.role === 'thumb' && m.state === 'done');
}

export function extraString(msg: Message, key: string): string {
  const v = msg.extra?.[key];
  return typeof v === 'string' ? v : '';
}

export function extraNumber(msg: Message, key: string): number {
  const v = msg.extra?.[key];
  return typeof v === 'number' ? v : 0;
}

/** Kinds that open in the media viewer. */
export const VISUAL_KINDS = ['photo', 'video', 'animation'];
