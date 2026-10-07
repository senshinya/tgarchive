import { Search, X } from 'lucide-preact';
import { useEffect, useRef } from 'preact/hooks';
import { botName, chatName } from '../../lib/format';
import { useStore } from '../../state/store';

/** The name of conversation key `key` (a chat, or -botId for a bot's timeline). */
export function useConvName(key: number): string {
  const store = useStore();
  if (key < 0) {
    const bot = store.botsById.value.get(-key);
    return bot ? botName(bot) : '机器人';
  }
  const chat = store.chats.value.find((c) => c.id === key);
  return chat ? chatName(chat) : '会话';
}

/** Web A style search field atop the left column; a chip shows when the search is limited to
 * one conversation. */
export function SearchBox() {
  const store = useStore();
  const input = useRef<HTMLInputElement>(null);
  const query = store.searchQuery.value;
  const scope = store.searchScope.value;
  const scopeName = useConvName(scope);
  const focus = store.searchFocus.value;
  useEffect(() => {
    if (focus) input.current?.focus();
  }, [focus]);
  const clear = () => {
    store.searchQuery.value = '';
    store.searchScope.value = 0;
  };
  return (
    <div class={`SearchInput${query || scope ? ' active' : ''}`}>
      <Search size={20} class="SearchInput-icon" />
      {scope !== 0 && (
        <button
          type="button"
          class="SearchInput-chip"
          aria-label={`取消限定：${scopeName}`}
          onClick={() => {
            store.searchScope.value = 0;
            input.current?.focus();
          }}
        >
          {scopeName}
          <X size={14} />
        </button>
      )}
      <input
        ref={input}
        type="search"
        aria-label="搜索消息"
        placeholder={scope ? '在此会话中搜索' : '搜索'}
        value={query}
        onInput={(e) => {
          store.searchQuery.value = (e.currentTarget as HTMLInputElement).value;
        }}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            clear();
            (e.currentTarget as HTMLInputElement).blur();
          }
        }}
      />
      {(query || scope !== 0) && (
        <button type="button" class="SearchInput-clear" aria-label="清除搜索" onClick={clear}>
          <X size={18} />
        </button>
      )}
    </div>
  );
}
