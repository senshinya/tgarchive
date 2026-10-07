import { mediaUrl } from '../../api/client';
import type { Message } from '../../api/types';
import { safeHref } from '../../lib/entities';
import { MediaStatus } from '../media/MediaStatus';

interface Preview {
  url?: string;
  display_url?: string;
  site_name?: string;
  title?: string;
  description?: string;
}

/** Web A's link preview card under a post's text: site, title, description and picture. */
export function LinkPreview({ msg }: { msg: Message }) {
  const lp = msg.extra?.link_preview as Preview | undefined;
  const href = lp ? safeHref(lp.url ?? '') : null;
  if (!lp || !href) return null;
  const photo = msg.media.find((m) => m.role === 'link_preview');
  return (
    <a class="WebPage" href={href} target="_blank" rel="noopener noreferrer">
      <span class="WebPage-text">
        <span class="WebPage-site">{lp.site_name || lp.display_url || href}</span>
        {lp.title && <span class="WebPage-title">{lp.title}</span>}
        {lp.description && <span class="WebPage-description">{lp.description}</span>}
      </span>
      {photo?.state === 'done' && (
        <img
          class="WebPage-photo"
          src={mediaUrl(photo.id)}
          alt=""
          loading="lazy"
          style={photo.width && photo.height ? { aspectRatio: `${photo.width} / ${photo.height}` } : undefined}
        />
      )}
      {/* Pending only: a failed download's retry button cannot sit inside a link. */}
      {photo?.state === 'pending' && (
        <span class="WebPage-pending">
          <MediaStatus msg={msg} media={photo} />
        </span>
      )}
    </a>
  );
}
