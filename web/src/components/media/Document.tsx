import { CircleAlert, Download } from 'lucide-preact';
import { mediaUrl } from '../../api/client';
import type { Media, Message } from '../../api/types';
import { fileColor, fileExtension, formatSize, kindLabel } from '../../lib/format';
import { Spinner } from '../../ui/Spinner';
import { RetryButton, inlineStatus } from './MediaStatus';
import { mainMedia, readyThumb } from './util';
import './media.scss';

/** File row: dog-eared extension tile (or thumbnail), name, size; downloads when archived. */
export function Document({ msg, media }: { msg: Message; media?: Media }) {
  const main = media ?? mainMedia(msg);
  if (!main) return null;
  const ext = fileExtension(main.file_name, main.mime);
  const thumb = readyThumb(msg);
  const done = main.state === 'done';
  const status = inlineStatus(main);
  const tile = (
    <>
      {thumb ? <img src={mediaUrl(thumb.id)} alt="" /> : <span class="File-ext">{ext}</span>}
      <span class="File-overlay">
        {main.state === 'pending' && <Spinner size={24} color="#fff" />}
        {(main.state === 'failed' || main.state === 'too_large') && <CircleAlert size={24} />}
        {done && <Download size={24} class="File-download" />}
      </span>
    </>
  );
  return (
    <div class="File">
      {done ? (
        <a
          class={`File-icon${thumb ? ' with-thumb' : ''}`}
          style={{ '--file-color': fileColor(ext) }}
          href={mediaUrl(main.id, true)}
          download={main.file_name || undefined}
          aria-label={`下载 ${main.file_name || kindLabel(msg.kind)}`}
        >
          {tile}
        </a>
      ) : (
        <div class={`File-icon${thumb ? ' with-thumb' : ''} not-ready`} style={{ '--file-color': fileColor(ext) }}>
          {tile}
        </div>
      )}
      <div class="File-info">
        <div class="File-title" title={main.file_name}>
          {main.file_name || kindLabel(msg.kind)}
        </div>
        <div class="File-subtitle">
          {status ? (
            <>
              <span class={main.state === 'pending' ? '' : 'File-error'}>{status}</span>
              {main.size > 0 && <span> · {formatSize(main.size)}</span>}
              {main.state === 'failed' && <RetryButton msg={msg} media={main} />}
            </>
          ) : (
            formatSize(main.size)
          )}
        </div>
      </div>
    </div>
  );
}
