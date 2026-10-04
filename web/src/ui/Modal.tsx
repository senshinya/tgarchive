import type { ComponentChildren } from 'preact';
import { useEffect } from 'preact/hooks';
import './ui.scss';

interface ModalProps {
  title: string;
  onClose: () => void;
  children: ComponentChildren;
}

export function Modal({ title, onClose, children }: ModalProps) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);
  return (
    <div class="Modal" onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div class="Modal-dialog" role="dialog" aria-modal="true" aria-label={title}>
        <h3 class="Modal-title">{title}</h3>
        {children}
      </div>
    </div>
  );
}

interface ConfirmProps {
  title: string;
  text: string;
  confirmLabel: string;
  danger?: boolean;
  busy?: boolean;
  children?: ComponentChildren;
  onConfirm: () => void;
  onClose: () => void;
}

export function ConfirmDialog({ title, text, confirmLabel, danger, busy, children, onConfirm, onClose }: ConfirmProps) {
  return (
    <Modal title={title} onClose={onClose}>
      <p class="Modal-text">{text}</p>
      {children}
      <div class="Modal-actions">
        <button type="button" class="Modal-action" onClick={onClose}>
          取消
        </button>
        <button type="button" class={`Modal-action${danger ? ' danger' : ''}`} disabled={busy} onClick={onConfirm}>
          {confirmLabel}
        </button>
      </div>
    </Modal>
  );
}
