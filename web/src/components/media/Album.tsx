import type { Message } from '../../api/types';
import { layoutAlbum } from '../../lib/album';
import { Audio } from './Audio';
import { Document } from './Document';
import { Photo } from './Photo';
import { Video } from './Video';
import { mainMedia } from './util';
import './media.scss';

interface Props {
  msgs: Message[];
  onOpen: (msg: Message) => void;
}

/** Media group: photo/video mosaic, or a stacked list for document/audio albums. */
export function Album({ msgs, onOpen }: Props) {
  const visual = msgs.every((m) => m.kind === 'photo' || m.kind === 'video');
  if (!visual) {
    return (
      <div class="Album-list">
        {msgs.map((m) => (
          <div class="Album-list-item" key={m.id} data-message-id={m.id}>
            {m.kind === 'audio' ? <Audio msg={m} /> : <Document msg={m} />}
          </div>
        ))}
      </div>
    );
  }
  const layout = layoutAlbum(msgs.map((m) => ({ width: mainMedia(m)?.width ?? 0, height: mainMedia(m)?.height ?? 0 })));
  return (
    <div
      class="media-inner Album"
      style={{ width: `${layout.width}px`, aspectRatio: `${layout.width} / ${layout.height}` }}
    >
      {msgs.map((m, i) => {
        const t = layout.tiles[i];
        return (
          <div
            key={m.id}
            class="Album-tile"
            data-message-id={m.id}
            style={{
              left: `${(t.x / layout.width) * 100}%`,
              top: `${(t.y / layout.height) * 100}%`,
              width: `${(t.w / layout.width) * 100}%`,
              height: `${(t.h / layout.height) * 100}%`,
            }}
          >
            {m.kind === 'video' ? <Video msg={m} fill onOpen={() => onOpen(m)} /> : <Photo msg={m} fill onOpen={() => onOpen(m)} />}
          </div>
        );
      })}
    </div>
  );
}
