import { X } from 'lucide-preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { errorMessage } from '../../api/client';
import type { TagCount } from '../../api/types';
import { useStore } from '../../state/store';
import { Modal } from '../../ui/Modal';
import './favorites.scss';

const MAX_SUGGESTIONS = 8;

/** Edits a favorite's tags: chips for the chosen ones, a field that adds on Enter, and the
 * existing tags matching what is typed. */
export function TagDialog({ initial, onSave, onClose }: { initial: string[]; onSave: (tags: string[]) => Promise<void>; onClose: () => void }) {
  const store = useStore();
  const [tags, setTags] = useState(initial);
  const [text, setText] = useState('');
  const [known, setKnown] = useState<TagCount[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => {
    // Not autoFocus: browsers honour that attribute only while the page loads.
    input.current?.focus();
    store.api.tags().then(setKnown, () => setKnown([]));
  }, []);

  const has = (name: string) => tags.some((t) => t.toLowerCase() === name.toLowerCase());
  const add = (raw: string) => {
    const name = raw.trim();
    if (name && !has(name)) setTags([...tags, name]);
    setText('');
  };
  const q = text.trim().toLowerCase();
  const suggestions = known.filter((t) => !has(t.name) && (!q || t.name.toLowerCase().startsWith(q))).slice(0, MAX_SUGGESTIONS);

  const save = async () => {
    setBusy(true);
    setError('');
    try {
      await onSave(text.trim() && !has(text.trim()) ? [...tags, text.trim()] : tags);
      onClose();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal title="标签" onClose={onClose}>
      <div class="TagDialog">
        <div class="TagDialog-chips">
          {tags.map((t) => (
            <button key={t} type="button" class="TagChip removable" aria-label={`移除标签 ${t}`} onClick={() => setTags(tags.filter((x) => x !== t))}>
              {t}
              <X size={14} aria-hidden="true" />
            </button>
          ))}
          <input
            ref={input}
            type="text"
            aria-label="添加标签"
            placeholder={tags.length ? '' : '输入标签，回车添加'}
            value={text}
            maxLength={32}
            onInput={(e) => setText((e.currentTarget as HTMLInputElement).value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault();
                add(text);
              } else if (e.key === 'Backspace' && !text && tags.length) setTags(tags.slice(0, -1));
            }}
          />
        </div>
        {suggestions.length > 0 && (
          <div class="TagDialog-suggestions">
            {suggestions.map((t) => (
              <button key={t.id} type="button" class="TagChip" aria-label={t.name} onClick={() => add(t.name)}>
                {t.name}
              </button>
            ))}
          </div>
        )}
        {error && <p class="TagDialog-error">{error}</p>}
      </div>
      <div class="Modal-actions">
        <button type="button" class="Modal-action" onClick={onClose}>
          取消
        </button>
        <button type="button" class="Modal-action" disabled={busy} onClick={() => void save()}>
          保存
        </button>
      </div>
    </Modal>
  );
}
