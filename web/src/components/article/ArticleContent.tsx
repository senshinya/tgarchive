import { ExternalLink } from 'lucide-preact';
import { Fragment, h, type ComponentChild } from 'preact';
import { mediaUrl } from '../../api/client';
import type { ArticleElement, ArticleMedia, ArticleNode } from '../../api/types';
import { safeHref } from '../../lib/entities';

/** Telegraph tags rendered as plain elements (b/i map to their semantic twins). Anything not
 * listed here and not handled below is unwrapped to its children: no attributes are ever copied
 * from the archive, and nothing goes through innerHTML. */
const BLOCKS: Record<string, string> = {
  p: 'p',
  h3: 'h3',
  h4: 'h4',
  blockquote: 'blockquote',
  aside: 'aside',
  ul: 'ul',
  ol: 'ol',
  li: 'li',
  pre: 'pre',
  code: 'code',
  b: 'strong',
  strong: 'strong',
  i: 'em',
  em: 'em',
  u: 'u',
  s: 's',
  figure: 'figure',
  figcaption: 'figcaption',
};

export interface ArticleContentProps {
  nodes: ArticleNode[];
  media: Map<number, ArticleMedia>;
  onOpenMedia: (mediaId: number) => void;
  onRetry: (mediaId: number) => void;
}

export function ArticleContent(props: ArticleContentProps) {
  return <>{props.nodes.map((n, i) => renderNode(n, i, props))}</>;
}

function renderNode(n: ArticleNode, key: number, ctx: ArticleContentProps): ComponentChild {
  if (typeof n === 'string') return n;
  const kids = (n.children ?? []).map((c, i) => renderNode(c, i, ctx));
  switch (n.tag) {
    case 'br':
      return <br key={key} />;
    case 'hr':
      return <hr key={key} />;
    case 'a': {
      const href = safeHref(n.attrs?.href ?? '');
      return href ? (
        <a key={key} href={href} target="_blank" rel="noopener noreferrer">
          {kids}
        </a>
      ) : (
        <span key={key}>{kids}</span>
      );
    }
    case 'img':
    case 'video':
      return <ArticleMediaBlock key={key} node={n} ctx={ctx} />;
    case 'embed':
      return <EmbedCard key={key} node={n} />;
  }
  const tag = BLOCKS[n.tag];
  return tag ? h(tag, { key }, kids) : <Fragment key={key}>{kids}</Fragment>;
}

function ArticleMediaBlock({ node, ctx }: { node: ArticleElement; ctx: ArticleContentProps }) {
  const id = Number(node.attrs?.['data-media-id'] ?? 0);
  const m = ctx.media.get(id);
  if (m && m.state === 'done') {
    if (m.kind === 'video') {
      return <video class="ArticleMedia" src={mediaUrl(id)} controls playsInline preload="metadata" />;
    }
    return (
      <button type="button" class="ArticleMedia ArticleMedia-photo" aria-label="查看图片" onClick={() => ctx.onOpenMedia(id)}>
        <img src={mediaUrl(id)} alt="" loading="lazy" decoding="async" />
      </button>
    );
  }
  const original = safeHref(node.attrs?.['data-src'] ?? '');
  const label = !m || m.state === 'pending' ? '正在下载…' : m.state === 'too_large' ? '文件超过存档上限' : '下载失败';
  return (
    <div class="ArticleMedia-placeholder">
      <span>{label}</span>
      {m?.state === 'failed' && (
        <button type="button" class="ArticleMedia-retry" onClick={() => ctx.onRetry(id)}>
          重试
        </button>
      )}
      {original && (
        <a href={original} target="_blank" rel="noopener noreferrer">
          原链接
        </a>
      )}
    </div>
  );
}

const EMBED_SOURCES: Record<string, string> = {
  'youtube.com': 'YouTube',
  'youtu.be': 'YouTube',
  'vimeo.com': 'Vimeo',
  'twitter.com': 'Twitter',
  'x.com': 'Twitter',
};

function EmbedCard({ node }: { node: ArticleElement }) {
  const href = safeHref(node.attrs?.href ?? '') ?? safeHref(node.attrs?.src ?? '');
  if (!href) return null;
  const host = new URL(href).hostname.replace(/^www\./, '');
  return (
    <a class="ArticleEmbed" href={href} target="_blank" rel="noopener noreferrer">
      <ExternalLink size={20} class="ArticleEmbed-icon" />
      <span class="ArticleEmbed-text">
        <span class="ArticleEmbed-source">{EMBED_SOURCES[host] ?? host}</span>
        <span class="ArticleEmbed-url">{href}</span>
      </span>
    </a>
  );
}
