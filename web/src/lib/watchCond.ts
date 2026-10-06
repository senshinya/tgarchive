import type { CondGroup, CondLeaf, CondNode } from '../api/types';

// Mirrors internal/watchcond: the server validates again, this only catches mistakes early.
export const MAX_DEPTH = 3;
export const MAX_LEAVES = 30;
export const MAX_TEXT = 100;
export const MIN_WINDOW = 1;
export const MAX_WINDOW = 1440;

export function isGroup(n: CondNode): n is CondGroup {
  return 'op' in n;
}

export type Metric = CondLeaf['metric'];

export const METRICS: { key: Metric; label: string }[] = [
  { key: 'reaction', label: '指定表情数' },
  { key: 'total', label: 'reaction 总数' },
  { key: 'views', label: '浏览量' },
  { key: 'forwards', label: '转发数' },
  { key: 'replies', label: '评论数' },
  { key: 'ratio', label: '比率' },
  { key: 'type', label: '内容类型' },
  { key: 'text', label: '文本' },
];

export const TYPE_LABELS: Record<string, string> = { photo: '图片', video: '视频', file: '文件', text: '纯文字' };

/** A fresh condition of the given metric with sensible defaults. */
export function newLeaf(metric: Metric, reaction = '👍'): CondLeaf {
  switch (metric) {
    case 'reaction':
      return { metric, key: reaction, cmp: 'gte', value: 10 };
    case 'total':
    case 'views':
    case 'forwards':
    case 'replies':
      return { metric, cmp: 'gte', value: metric === 'views' ? 1000 : 10 };
    case 'ratio':
      return { metric, num: reaction, den: 'total', cmp: 'gte', value: 50 };
    case 'type':
      return { metric, cmp: 'is', value: 'photo' };
    case 'text':
      return { metric, cmp: 'contains', value: '' };
  }
}

export function defaultCond(reaction = '👍'): CondGroup {
  return { op: 'and', items: [newLeaf('reaction', reaction)] };
}

function leafError(l: CondLeaf): string | null {
  switch (l.metric) {
    case 'reaction':
      if (!l.key) return '请选择表情';
      return countError(l.value);
    case 'total':
    case 'views':
    case 'forwards':
    case 'replies':
      return countError(l.value);
    case 'ratio':
      if (!l.num) return '请选择表情';
      if (l.num === 'total' && l.den === 'total') return '比率的分子和分母不能相同';
      return Number.isFinite(l.value) && l.value >= 0 && l.value <= 100 ? null : '比率须为 0–100 之间的百分数';
    case 'type':
      return null;
    case 'text': {
      const n = [...l.value.trim()].length;
      return n >= 1 && n <= MAX_TEXT ? null : `关键词须为 1–${MAX_TEXT} 个字符`;
    }
  }
}

function countError(v: number): string | null {
  return Number.isInteger(v) && v >= 0 ? null : '数量须为非负整数';
}

/** The first problem in the tree, as the server would phrase it; null when it is valid. Paths of
 * the offending nodes are collected into `bad` (e.g. "0.2") so the editor can mark them. */
export function validateCond(root: CondGroup, bad?: Set<string>): string | null {
  let first: string | null = null;
  let leaves = 0;
  const report = (path: string, msg: string) => {
    bad?.add(path);
    first ??= msg;
  };
  const walk = (n: CondNode, path: string, depth: number) => {
    if (isGroup(n)) {
      if (depth > MAX_DEPTH) report(path, `条件组最多嵌套 ${MAX_DEPTH} 层`);
      if (n.items.length === 0) report(path, '条件组不能为空');
      n.items.forEach((c, i) => walk(c, path ? `${path}.${i}` : String(i), depth + 1));
      return;
    }
    leaves++;
    const err = leafError(n);
    if (err) report(path, err);
  };
  walk(root, '', 1);
  if (leaves > MAX_LEAVES) first ??= `条件最多 ${MAX_LEAVES} 个`;
  return first;
}

/** Counts the conditions in a tree. */
export function countLeaves(n: CondNode): number {
  return isGroup(n) ? n.items.reduce((a, c) => a + countLeaves(c), 0) : 1;
}

/** Telegram-style counter: 999, 1.2K, 12K, 1.5M. */
export function formatCount(v: number): string {
  if (v < 1000) return String(v);
  const short = (f: number) => (f >= 10 ? String(Math.floor(f)) : (Math.floor(f * 10) / 10).toString());
  if (v < 1_000_000) return `${short(v / 1000)}K`;
  return `${short(v / 1_000_000)}M`;
}

/** Drops emoji variation selectors, as the server does when matching reactions. */
export function normKey(k: string): string {
  return k.replace(/️/g, '');
}
