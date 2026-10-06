import { Plus, X } from 'lucide-preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import type { CondGroup, CondLeaf, CondNode, ReactionStat } from '../../api/types';
import { MAX_DEPTH, METRICS, TYPE_LABELS, isGroup, newLeaf, normKey, type Metric } from '../../lib/watchCond';
import { ReactionIcon, reactionLabel } from './ReactionIcon';
import './watch.scss';

export interface Reactions {
  all: boolean;
  list: ReactionStat[];
}

interface Ctx {
  reactions: Reactions;
  bad: Set<string>;
}

/** Notion-style editor for a watch's condition tree (spec §6). */
export function CondEditor({ value, onChange, reactions, bad }: { value: CondGroup; onChange: (g: CondGroup) => void; reactions: Reactions; bad: Set<string> }) {
  return (
    <div class="CondEditor">
      <GroupEditor group={value} path="" depth={1} onChange={(g) => onChange(g as CondGroup)} ctx={{ reactions, bad }} />
    </div>
  );
}

function firstReaction(r: Reactions): string {
  return r.list[0]?.key ?? '👍';
}

function GroupEditor({ group, path, depth, onChange, onRemove, ctx }: {
  group: CondGroup;
  path: string;
  depth: number;
  onChange: (g: CondNode) => void;
  onRemove?: () => void;
  ctx: Ctx;
}) {
  const setItem = (i: number, n: CondNode) => onChange({ ...group, items: group.items.map((c, j) => (j === i ? n : c)) });
  const removeItem = (i: number) => onChange({ ...group, items: group.items.filter((_, j) => j !== i) });
  const add = (n: CondNode) => onChange({ ...group, items: [...group.items, n] });
  const childPath = (i: number) => (path ? `${path}.${i}` : String(i));
  return (
    <div class={`CondGroup depth-${depth}${ctx.bad.has(path) ? ' invalid' : ''}`}>
      <div class="CondGroup-head">
        <div class="CondOp" role="group" aria-label="组合方式">
          {(['and', 'or'] as const).map((op) => (
            <button key={op} type="button" class={group.op === op ? 'active' : ''} aria-pressed={group.op === op} onClick={() => onChange({ ...group, op })}>
              {op === 'and' ? '全部满足' : '任一满足'}
            </button>
          ))}
        </div>
        {onRemove && (
          <button type="button" class="CondRemove" aria-label="删除条件组" title="删除条件组" onClick={onRemove}>
            <X size={18} />
          </button>
        )}
      </div>
      {group.items.length === 0 && <div class="CondEmpty">条件组不能为空</div>}
      {group.items.map((item, i) =>
        isGroup(item) ? (
          <GroupEditor key={i} group={item} path={childPath(i)} depth={depth + 1} onChange={(n) => setItem(i, n)} onRemove={() => removeItem(i)} ctx={ctx} />
        ) : (
          <LeafEditor key={i} leaf={item} invalid={ctx.bad.has(childPath(i))} onChange={(n) => setItem(i, n)} onRemove={() => removeItem(i)} ctx={ctx} />
        ),
      )}
      <div class="CondGroup-actions">
        <button type="button" class="CondAdd" onClick={() => add(newLeaf('reaction', firstReaction(ctx.reactions)))}>
          <Plus size={16} />
          条件
        </button>
        {depth < MAX_DEPTH && (
          <button type="button" class="CondAdd" onClick={() => add({ op: group.op === 'and' ? 'or' : 'and', items: [newLeaf('reaction', firstReaction(ctx.reactions))] })}>
            <Plus size={16} />
            条件组
          </button>
        )}
      </div>
    </div>
  );
}

function NumberInput({ value, onChange, label, suffix }: { value: number; onChange: (v: number) => void; label: string; suffix?: string }) {
  const [text, setText] = useState(String(value));
  useEffect(() => {
    if (Number(text) !== value) setText(String(value));
  }, [value]);
  return (
    <span class="CondNumber">
      <input
        type="number"
        inputMode="decimal"
        min={0}
        aria-label={label}
        value={text}
        onInput={(e) => {
          const t = (e.currentTarget as HTMLInputElement).value;
          setText(t);
          onChange(t.trim() === '' ? NaN : Number(t));
        }}
      />
      {suffix && <span class="CondNumber-suffix">{suffix}</span>}
    </span>
  );
}

function Select<T extends string>({ value, options, onChange, label }: { value: T; options: { key: T; label: string }[]; onChange: (v: T) => void; label: string }) {
  return (
    <select class="CondSelect" aria-label={label} value={value} onChange={(e) => onChange((e.currentTarget as HTMLSelectElement).value as T)}>
      {options.map((o) => (
        <option key={o.key} value={o.key}>
          {o.label}
        </option>
      ))}
    </select>
  );
}

const COUNT_CMP = [
  { key: 'gte' as const, label: '≥' },
  { key: 'lte' as const, label: '≤' },
];

function LeafEditor({ leaf, invalid, onChange, onRemove, ctx }: { leaf: CondLeaf; invalid: boolean; onChange: (l: CondLeaf) => void; onRemove: () => void; ctx: Ctx }) {
  const setMetric = (m: Metric) => {
    if (m !== leaf.metric) onChange(newLeaf(m, firstReaction(ctx.reactions)));
  };
  let fields;
  switch (leaf.metric) {
    case 'reaction':
      fields = (
        <>
          <ReactionPicker value={leaf.key} reactions={ctx.reactions} onChange={(key) => onChange({ ...leaf, key })} />
          <Select label="比较" value={leaf.cmp} options={COUNT_CMP} onChange={(cmp) => onChange({ ...leaf, cmp })} />
          <NumberInput label="数量" value={leaf.value} onChange={(value) => onChange({ ...leaf, value })} />
        </>
      );
      break;
    case 'total':
    case 'views':
    case 'forwards':
    case 'replies':
      fields = (
        <>
          <Select label="比较" value={leaf.cmp} options={COUNT_CMP} onChange={(cmp) => onChange({ ...leaf, cmp })} />
          <NumberInput label="数量" value={leaf.value} onChange={(value) => onChange({ ...leaf, value })} />
        </>
      );
      break;
    case 'ratio':
      fields = (
        <>
          <ReactionPicker value={leaf.num} reactions={ctx.reactions} withTotal onChange={(num) => onChange({ ...leaf, num })} />
          <span class="CondText">/</span>
          <Select
            label="分母"
            value={leaf.den}
            options={[
              { key: 'total', label: 'reaction 总数' },
              { key: 'views', label: '浏览量' },
            ]}
            onChange={(den) => onChange({ ...leaf, den })}
          />
          <Select label="比较" value={leaf.cmp} options={COUNT_CMP} onChange={(cmp) => onChange({ ...leaf, cmp })} />
          <NumberInput label="百分比" suffix="%" value={leaf.value} onChange={(value) => onChange({ ...leaf, value })} />
        </>
      );
      break;
    case 'type':
      fields = (
        <>
          <Select
            label="比较"
            value={leaf.cmp}
            options={[
              { key: 'is', label: '是' },
              { key: 'not', label: '不是' },
            ]}
            onChange={(cmp) => onChange({ ...leaf, cmp })}
          />
          <Select
            label="类型"
            value={leaf.value}
            options={(Object.keys(TYPE_LABELS) as (keyof typeof TYPE_LABELS)[]).map((k) => ({ key: k as 'photo', label: TYPE_LABELS[k] }))}
            onChange={(value) => onChange({ ...leaf, value })}
          />
        </>
      );
      break;
    case 'text':
      fields = (
        <>
          <Select
            label="比较"
            value={leaf.cmp}
            options={[
              { key: 'contains', label: '包含' },
              { key: 'not_contains', label: '不包含' },
            ]}
            onChange={(cmp) => onChange({ ...leaf, cmp })}
          />
          <input
            class="CondInput"
            aria-label="关键词"
            placeholder="关键词"
            maxLength={100}
            value={leaf.value}
            onInput={(e) => onChange({ ...leaf, value: (e.currentTarget as HTMLInputElement).value })}
          />
        </>
      );
      break;
  }
  return (
    <div class={`CondLeaf${invalid ? ' invalid' : ''}`}>
      <Select label="指标" value={leaf.metric} options={METRICS} onChange={setMetric} />
      {fields}
      <button type="button" class="CondRemove" aria-label="删除条件" title="删除条件" onClick={onRemove}>
        <X size={18} />
      </button>
    </div>
  );
}

/** Picks a reaction from the channel's allowed set; channels allowing any emoji also take typed ones. */
export function ReactionPicker({ value, reactions, withTotal, onChange }: { value: string; reactions: Reactions; withTotal?: boolean; onChange: (key: string) => void }) {
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState('');
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const close = (e: Event) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('pointerdown', close);
    return () => document.removeEventListener('pointerdown', close);
  }, [open]);
  const current = reactions.list.find((r) => r.key === value);
  const pick = (k: string) => {
    onChange(k);
    setOpen(false);
  };
  return (
    <div class="ReactionPicker" ref={ref}>
      <button type="button" class="ReactionPicker-button" aria-haspopup="listbox" aria-expanded={open} aria-label="选择表情" onClick={() => setOpen(!open)}>
        {value === 'total' ? 'reaction 总数' : <ReactionIcon stat={current ?? { key: value, emoji: value }} />}
      </button>
      {open && (
        <div class="ReactionPicker-menu" role="listbox">
          {withTotal && (
            <button type="button" class="ReactionPicker-total" role="option" aria-selected={value === 'total'} onClick={() => pick('total')}>
              reaction 总数
            </button>
          )}
          <div class="ReactionPicker-grid">
            {reactions.list.map((r) => (
              <button key={r.key} type="button" role="option" aria-selected={r.key === value} title={reactionLabel(r.key)} onClick={() => pick(r.key)}>
                <ReactionIcon stat={r} />
              </button>
            ))}
            <button type="button" role="option" aria-selected={value === 'paid'} title="付费星星" onClick={() => pick('paid')}>
              <ReactionIcon stat={{ key: 'paid' }} />
            </button>
          </div>
          {reactions.all && (
            <form
              class="ReactionPicker-typed"
              onSubmit={(e) => {
                e.preventDefault();
                const k = normKey(typed.trim());
                if (k) pick(k);
                setTyped('');
              }}
            >
              <input aria-label="其他表情" placeholder="输入其他表情" value={typed} onInput={(e) => setTyped((e.currentTarget as HTMLInputElement).value)} />
              <button type="submit">确定</button>
            </form>
          )}
        </div>
      )}
    </div>
  );
}
