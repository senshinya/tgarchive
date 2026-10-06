import type { ComponentChildren } from 'preact';
import { Spinner } from './Spinner';
import './ui.scss';

interface Props {
  children: ComponentChildren;
  variant?: 'primary' | 'secondary' | 'danger';
  type?: 'button' | 'submit';
  disabled?: boolean;
  loading?: boolean;
  onClick?: () => void;
}

export function Button({ children, variant = 'primary', type = 'button', disabled, loading, onClick }: Props) {
  return (
    <button type={type} class={`Button ${variant}`} disabled={disabled || loading} onClick={onClick}>
      {loading ? <Spinner size={24} /> : children}
    </button>
  );
}

interface IconButtonProps {
  label: string;
  children: ComponentChildren;
  onClick?: (e: MouseEvent) => void;
  class?: string;
  disabled?: boolean;
  /** For a toggle: whether it is on (aria-pressed). */
  pressed?: boolean;
}

/** Round 40px translucent icon button used in headers. */
export function IconButton({ label, children, onClick, class: cls = '', disabled, pressed }: IconButtonProps) {
  return (
    <button
      type="button"
      class={`IconButton ${cls}`}
      aria-label={label}
      aria-pressed={pressed}
      title={label}
      onClick={onClick}
      disabled={disabled}
    >
      {children}
    </button>
  );
}
