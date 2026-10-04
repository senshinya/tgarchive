import type { ComponentChildren } from 'preact';
import { useMemo, useState } from 'preact/hooks';
import type { Entity } from '../../api/types';
import { buildTree, safeHref, type RichNode } from '../../lib/entities';
import './RichText.scss';

function Spoiler({ children }: { children: ComponentChildren }) {
  const [revealed, setRevealed] = useState(false);
  return (
    <span
      class={`text-entity-spoiler${revealed ? ' revealed' : ''}`}
      role={revealed ? undefined : 'button'}
      aria-label={revealed ? undefined : '显示剧透内容'}
      onClick={(e) => {
        if (!revealed) {
          e.preventDefault();
          e.stopPropagation();
          setRevealed(true);
        }
      }}
    >
      <span class="spoiler-content">{children}</span>
    </span>
  );
}

function ExpandableQuote({ children }: { children: ComponentChildren }) {
  const [expanded, setExpanded] = useState(false);
  return (
    <blockquote class={`text-entity-quote expandable${expanded ? ' expanded' : ''}`}>
      <span class="quote-body">{children}</span>
      <button type="button" class="quote-toggle" aria-label={expanded ? '收起引用' : '展开引用'} onClick={() => setExpanded(!expanded)}>
        {expanded ? '收起' : '展开'}
      </button>
    </blockquote>
  );
}

function Link({ href, children }: { href: string | null; children: ComponentChildren }) {
  if (!href) return <span class="text-entity-link">{children}</span>;
  return (
    <a class="text-entity-link" href={href} target="_blank" rel="noopener noreferrer" onClick={(e) => e.stopPropagation()}>
      {children}
    </a>
  );
}

function renderNode(node: RichNode, key: number): ComponentChildren {
  if (node.kind === 'text') return node.text;
  const kids = node.children.map(renderNode);
  const e = node.entity;
  switch (e.type) {
    case 'bold':
      return <strong key={key}>{kids}</strong>;
    case 'italic':
      return <em key={key}>{kids}</em>;
    case 'underline':
      return <u key={key}>{kids}</u>;
    case 'strikethrough':
      return <del key={key}>{kids}</del>;
    case 'spoiler':
      return <Spoiler key={key}>{kids}</Spoiler>;
    case 'code':
      return (
        <code key={key} class="text-entity-code">
          {kids}
        </code>
      );
    case 'pre':
      return (
        <pre key={key} class="text-entity-pre" data-language={e.language || undefined}>
          {e.language && <span class="code-language">{e.language}</span>}
          <code>{kids}</code>
        </pre>
      );
    case 'blockquote':
      return (
        <blockquote key={key} class="text-entity-quote">
          {kids}
        </blockquote>
      );
    case 'expandable_blockquote':
      return <ExpandableQuote key={key}>{kids}</ExpandableQuote>;
    case 'url':
      return (
        <Link key={key} href={safeHref(node.text)}>
          {kids}
        </Link>
      );
    case 'text_link':
      return (
        <Link key={key} href={safeHref(e.url ?? '')}>
          {kids}
        </Link>
      );
    case 'mention':
      return (
        <Link key={key} href={safeHref(`https://t.me/${node.text.replace(/^@/, '')}`)}>
          {kids}
        </Link>
      );
    case 'email':
      return (
        <Link key={key} href={safeHref(`mailto:${node.text}`)}>
          {kids}
        </Link>
      );
    case 'phone_number':
      return (
        <Link key={key} href={safeHref(`tel:${node.text.replace(/[^0-9+]/g, '')}`)}>
          {kids}
        </Link>
      );
    case 'text_mention':
    case 'hashtag':
    case 'cashtag':
    case 'bot_command':
      return (
        <span key={key} class="text-entity-link">
          {kids}
        </span>
      );
    case 'custom_emoji':
      // The text under a custom emoji entity is its fallback emoji.
      return (
        <span key={key} class="custom-emoji" data-custom-emoji-id={e.custom_emoji_id}>
          {kids}
        </span>
      );
    default:
      return <span key={key}>{kids}</span>;
  }
}

export function RichText({ text, entities }: { text: string; entities: Entity[] }) {
  const nodes = useMemo(() => buildTree(text, entities ?? []), [text, entities]);
  return <>{nodes.map(renderNode)}</>;
}
