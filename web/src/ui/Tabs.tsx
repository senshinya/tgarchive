import type { ComponentChildren } from 'preact';
import './ui.scss';

export interface TabItem<K extends string | number> {
  key: K;
  label: ComponentChildren;
}

interface Props<K extends string | number> {
  items: TabItem<K>[];
  active: K;
  onChange: (key: K) => void;
  class?: string;
}

export function Tabs<K extends string | number>({ items, active, onChange, class: cls = '' }: Props<K>) {
  return (
    <div class={`Tabs ${cls}`} role="tablist">
      {items.map((t) => (
        <button
          key={t.key}
          type="button"
          role="tab"
          aria-selected={t.key === active}
          class={`Tab${t.key === active ? ' active' : ''}`}
          onClick={() => onChange(t.key)}
        >
          <span class="Tab-label">
            {t.label}
            {t.key === active && <i class="Tab-indicator" aria-hidden="true" />}
          </span>
        </button>
      ))}
    </div>
  );
}
