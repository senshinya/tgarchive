import type { Media, Message, MessageKind } from '../../api/types';

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

/** Image formats every browser renders; a GIF stays animated in an <img>. */
const IMAGE_DOCUMENT_MIMES = ['image/gif', 'image/jpeg', 'image/png', 'image/webp'];
/** Larger image files stay file rows rather than being pulled into the timeline. */
export const IMAGE_DOCUMENT_MAX_BYTES = 20 * 1024 * 1024;

/** How a message is shown: an image sent as a file (say a GIF Telegram did not convert) shows,
 * and opens in the viewer, as a photo. */
export function displayKind(msg: Message): MessageKind {
  if (msg.kind !== 'document') return msg.kind;
  const main = mainMedia(msg);
  return main && IMAGE_DOCUMENT_MIMES.includes(main.mime) && main.size <= IMAGE_DOCUMENT_MAX_BYTES ? 'photo' : msg.kind;
}
