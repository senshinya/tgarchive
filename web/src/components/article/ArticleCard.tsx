import { mediaUrl } from '../../api/client';
import type { Message } from '../../api/types';
import { safeHref } from '../../lib/entities';
import { articleRoute, navigate, route, routeConvKey } from '../../lib/router';
import './article.scss';

/** Link-preview style card under a Telegraph link message; opens the reader once archived. */
export function ArticleCard({ msg }: { msg: Message }) {
  const a = msg.article;
  if (!a) return null;
  if (a.state === 'fetched') {
    return (
      <button
        type="button"
        class="ArticleCard"
        onClick={() => navigate(articleRoute(routeConvKey(route.value) || msg.chat_id, msg.id), { fromChat: true })}
      >
        <span class="ArticleCard-site">Telegraph</span>
        {a.title && <span class="ArticleCard-title">{a.title}</span>}
        {a.description && <span class="ArticleCard-description">{a.description}</span>}
        {a.image_media_id ? (
          <img class="ArticleCard-image" src={mediaUrl(a.image_media_id)} alt="" loading="lazy" decoding="async" />
        ) : null}
      </button>
    );
  }
  const href = safeHref(a.url);
  return (
    <div class="ArticleCard">
      <span class="ArticleCard-site">Telegraph</span>
      {a.state === 'failed' ? (
        <>
          <span class="ArticleCard-status error">存档失败：{a.error || '未知原因'}</span>
          {href && (
            <a class="ArticleCard-link" href={href} target="_blank" rel="noopener noreferrer">
              {href}
            </a>
          )}
        </>
      ) : (
        <span class="ArticleCard-status">正在存档文章…</span>
      )}
    </div>
  );
}
