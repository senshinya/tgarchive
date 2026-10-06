import { mediaUrl } from '../../api/client';
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

/** canPlayType probes for the codecs a server-side copy can stand in for; any other codec with a
 * copy (MPEG-4 Part 2, WMV, ...) plays in no browser. */
const CODEC_PROBES: Record<string, string> = {
  av1: 'video/mp4; codecs="av01.0.08M.08"',
  hevc: 'video/mp4; codecs="hvc1.1.6.L93.B0"',
  vp9: 'video/mp4; codecs="vp09.00.10.08"',
};

let probe: HTMLVideoElement | undefined;

export function canPlayCodec(codec: string): boolean {
  const type = CODEC_PROBES[codec];
  if (!type || typeof document === 'undefined') return false;
  probe ??= document.createElement('video');
  return probe.canPlayType(type) !== '';
}

/** What a <video> plays: the archived file, or the server's H.264 copy when this browser cannot
 * decode the original. Downloads always use mediaUrl(id, true), the original. */
export function playUrl(id: number, compatCodec?: string): string {
  return compatCodec && !canPlayCodec(compatCodec) ? `/media/${id}?compat=1` : mediaUrl(id);
}
