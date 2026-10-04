import type { ComponentChildren } from 'preact';
import './ui.scss';

interface Props {
  icon?: ComponentChildren;
  title: ComponentChildren;
  subtitle?: ComponentChildren;
  right?: ComponentChildren;
  danger?: boolean;
  onClick?: () => void;
}

/** Settings row sharing the chat-list row geometry (min-height 48px, 16px radius). */
export function ListItem({ icon, title, subtitle, right, danger, onClick }: Props) {
  const body = (
    <>
      {icon && <span class="ListItem-icon">{icon}</span>}
      <span class="ListItem-text">
        <span class="ListItem-title">{title}</span>
        {subtitle && <span class="ListItem-subtitle">{subtitle}</span>}
      </span>
      {right && <span class="ListItem-right">{right}</span>}
    </>
  );
  const cls = `ListItem${danger ? ' danger' : ''}${onClick ? ' interactive' : ''}`;
  return onClick ? (
    <button type="button" class={cls} onClick={onClick}>
      {body}
    </button>
  ) : (
    <div class={cls}>{body}</div>
  );
}
