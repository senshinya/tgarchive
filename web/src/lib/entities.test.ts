import { describe, expect, it } from 'vitest';
import type { Entity } from '../api/types';
import { buildTree, extractLinks, safeHref, type RichNode } from './entities';

const e = (type: string, offset: number, length: number, extra: Partial<Entity> = {}): Entity => ({
  type,
  offset,
  length,
  ...extra,
});

// Compact serialisation: entities as type(children), text as-is.
function show(nodes: RichNode[]): string {
  return nodes.map((n) => (n.kind === 'text' ? n.text : `${n.entity.type}(${show(n.children)})`)).join('');
}

describe('buildTree', () => {
  it('returns plain text when there are no entities', () => {
    expect(show(buildTree('hello', []))).toBe('hello');
  });

  it('nests entities that share a range and contained ones', () => {
    const text = 'bold italic tail';
    const tree = buildTree(text, [e('italic', 5, 6), e('bold', 0, 11)]);
    expect(show(tree)).toBe('bold(bold italic(italic)) tail');
  });

  it('splits partially overlapping entities at the outer boundary', () => {
    // bold "abcd", italic "cdef"
    const tree = buildTree('abcdef', [e('bold', 0, 4), e('italic', 2, 4)]);
    expect(show(tree)).toBe('bold(abitalic(cd))italic(ef)');
  });

  it('uses UTF-16 offsets so astral emoji count as two units', () => {
    const text = '👍 ok';
    const tree = buildTree(text, [e('bold', 3, 2)]);
    expect(show(tree)).toBe('👍 bold(ok)');
  });

  it('puts block entities outside inline ones on the same range', () => {
    const tree = buildTree('code', [e('bold', 0, 4), e('pre', 0, 4, { language: 'go' })]);
    expect(show(tree)).toBe('pre(bold(code))');
  });

  it('drops empty, negative and clamps out-of-range entities', () => {
    const tree = buildTree('abc', [e('bold', 1, 0), e('italic', -1, 2), e('underline', 2, 50)]);
    expect(show(tree)).toBe('abunderline(c)');
  });
});

describe('links', () => {
  it('accepts web, tg, mail and tel links and adds https to bare hosts', () => {
    expect(safeHref('https://x.dev/a')).toBe('https://x.dev/a');
    expect(safeHref('x.dev')).toBe('https://x.dev/');
    expect(safeHref('tg://resolve?domain=a')).toBe('tg://resolve?domain=a');
    expect(safeHref('mailto:a@b.c')).toBe('mailto:a@b.c');
  });

  it('rejects script and data URLs', () => {
    expect(safeHref('javascript:alert(1)')).toBeNull();
    expect(safeHref('data:text/html,hi')).toBeNull();
    expect(safeHref('')).toBeNull();
  });

  it('extracts url and text_link targets in order without duplicates', () => {
    const text = 'see x.dev and here and x.dev';
    const links = extractLinks(text, [
      e('url', 23, 5),
      e('text_link', 14, 4, { url: 'https://y.dev' }),
      e('url', 4, 5),
      e('text_link', 0, 3, { url: 'javascript:1' }),
    ]);
    expect(links).toEqual(['https://x.dev/', 'https://y.dev/']);
  });
});
