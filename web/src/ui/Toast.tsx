import { useEffect } from 'preact/hooks';
import { useStore } from '../state/store';
import './ui.scss';

export const TOAST_MS = 4000;

export function Toast() {
  const { toast } = useStore();
  const current = toast.value;
  useEffect(() => {
    if (!current) return;
    const t = setTimeout(() => {
      if (toast.value?.id === current.id) toast.value = null;
    }, TOAST_MS);
    return () => clearTimeout(t);
  }, [current?.id]);
  if (!current) return null;
  return (
    <div class="Toast" role="status">
      {current.text}
    </div>
  );
}
