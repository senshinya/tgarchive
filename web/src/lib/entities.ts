import type { Entity } from '../api/types';

export type RichNode =
  | { kind: 'text'; text: string }
  | { kind: 'entity'; entity: Entity; text: string; children: RichNode[] };

// Block-level entities sort outside inline ones when they share a range.
const NESTING_ORDER = ['pre', 'blockquote', 'expandable_blockquote', 'text_link', 'url', 'spoiler'];

function rank(type: string): number {
  const i = NESTING_ORDER.indexOf(type);
  return i === -1 ? NESTING_ORDER.length : i;
}

function byStart(a: Entity, b: Entity): number {
  return a.offset - b.offset || b.length - a.length || rank(a.type) - rank(b.type);
}

/** Drops malformed entities (non-finite, negative, empty) and clamps length to the text bound. */
function clampEntities(text: string, entities: Entity[]): Entity[] {
  return entities
    .filter((e) => Number.isFinite(e.offset) && Number.isFinite(e.length) && e.length > 0 && e.offset >= 0)
    .map((e) => ({ ...e, length: Math.min(e.length, text.length - e.offset) }))
    .filter((e) => e.length > 0);
}

/**
 * Turns Telegram text + entities (UTF-16 offsets, which match JS string indexing) into a
 * properly nested tree. Entities that partially overlap are split at the outer boundary.
 */
export function buildTree(text: string, entities: Entity[]): RichNode[] {
  return build(text, 0, text.length, clampEntities(text, entities).sort(byStart));
}

function build(text: string, start: number, end: number, sorted: Entity[]): RichNode[] {
  const out: RichNode[] = [];
  let pos = start;
  let queue = sorted;
  while (queue.length > 0) {
    const [head, ...others] = queue;
    const headEnd = head.offset + head.length;
    if (head.offset > pos) out.push({ kind: 'text', text: text.slice(pos, head.offset) });
    const inner: Entity[] = [];
    const rest: Entity[] = [];
    for (const e of others) {
      const eEnd = e.offset + e.length;
      if (e.offset >= headEnd) {
        rest.push(e);
      } else if (eEnd <= headEnd) {
        inner.push(e);
      } else {
        inner.push({ ...e, length: headEnd - e.offset });
        rest.push({ ...e, offset: headEnd, length: eEnd - headEnd });
      }
    }
    out.push({
      kind: 'entity',
      entity: head,
      text: text.slice(head.offset, headEnd),
      // A split can leave `inner` out of order relative to the (possibly truncated) lengths
      // that decide nesting for ties, so re-sort before recursing.
      children: build(text, head.offset, headEnd, inner.sort(byStart)),
    });
    pos = headEnd;
    queue = rest.sort(byStart);
  }
  if (pos < end) out.push({ kind: 'text', text: text.slice(pos, end) });
  return out;
}

const SAFE_PROTOCOLS = ['http:', 'https:', 'tg:', 'mailto:', 'tel:'];

/** Returns a navigable href or null for anything that is not an allowed scheme (e.g. javascript:). */
export function safeHref(raw: string): string | null {
  const v = raw.trim();
  if (!v) return null;
  const withScheme = /^[a-z][a-z0-9+.-]*:/i.test(v) ? v : `https://${v}`;
  try {
    const u = new URL(withScheme);
    return SAFE_PROTOCOLS.includes(u.protocol) ? u.href : null;
  } catch {
    return null;
  }
}

/** All link targets in a message (url + text_link entities), in order, de-duplicated. */
export function extractLinks(text: string, entities: Entity[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const e of clampEntities(text, entities).sort(byStart)) {
    let href: string | null = null;
    if (e.type === 'url') href = safeHref(text.slice(e.offset, e.offset + e.length));
    else if (e.type === 'text_link' && e.url) href = safeHref(e.url);
    if (href && !seen.has(href)) {
      seen.add(href);
      out.push(href);
    }
  }
  return out;
}
