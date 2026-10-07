// Stand-in for viewer/Gallery in jsdom (PhotoSwipe and Vidstack need a real browser): shows what
// the shell handed it and exposes the same paging and close callbacks.
import { useState } from 'preact/hooks';
import type { GalleryProps } from '../components/viewer/Gallery';

export function Gallery({ items, mediaId, titleOf, onClosed }: GalleryProps) {
  // Tracks the shown item by media id, like the real gallery, so a longer list arriving later
  // keeps it in place.
  const [shown, setShown] = useState(mediaId);
  const i = Math.max(0, items.findIndex((it) => it.mediaId === shown));
  const setI = (n: number) => setShown(items[n].mediaId);
  const it = items[i];
  return (
    <div class="StubGallery">
      <span class="MediaViewer-name">{titleOf(it)}</span>
      <span>{`${i + 1} / ${items.length}`}</span>
      {it.text && <span>{it.text}</span>}
      <span class="current" data-media={it.mediaId} data-kind={it.kind} data-thumb={it.thumbId ?? ''} data-size={`${it.width}x${it.height}`} />
      <button type="button" aria-label="上一个" onClick={() => setI(i - 1)} disabled={i === 0} />
      <button type="button" aria-label="下一个" onClick={() => setI(i + 1)} disabled={i === items.length - 1} />
      <button type="button" aria-label="关闭" onClick={onClosed} />
    </div>
  );
}

/** The item the stub is showing. */
export function current(container: Element) {
  return container.querySelector('.current') as HTMLElement;
}
