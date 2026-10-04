import { computed, signal } from '@preact/signals';
import { createContext } from 'preact';
import { useContext } from 'preact/hooks';
import { ApiError, PAGE_SIZE, errorMessage, type Api } from '../api/client';
import type { ArchiveEvent, Bot, Chat, Message } from '../api/types';

export interface Conversation {
  items: Message[]; // ascending by id
  hasMore: boolean;
  loading: boolean;
  loaded: boolean;
  error: string;
}

const EMPTY: Conversation = { items: [], hasMore: true, loading: false, loaded: false, error: '' };

export interface ViewerTarget {
  chatId: number;
  messageId: number;
  mediaId: number;
}

export interface Toast {
  id: number;
  text: string;
}

function mergeById(a: Message[], b: Message[]): Message[] {
  const map = new Map<number, Message>();
  for (const m of a) map.set(m.id, m);
  for (const m of b) map.set(m.id, m);
  return [...map.values()].sort((x, y) => x.id - y.id);
}

export function createStore(api: Api, opts: { chatsReloadDelay?: number } = {}) {
  const delay = opts.chatsReloadDelay ?? 300;
  const bots = signal<Bot[]>([]);
  const chats = signal<Chat[]>([]);
  const chatsLoaded = signal(false);
  const botFilter = signal(0); // 0 = 全部
  const conversations = signal<Record<number, Conversation>>({});
  const toast = signal<Toast | null>(null);
  const viewer = signal<ViewerTarget | null>(null); // media viewer target
  const sharedMediaOpen = signal(false); // right column
  let toastSeq = 0;
  let reloadTimer: ReturnType<typeof setTimeout> | undefined;

  const botsById = computed(() => new Map(bots.value.map((b) => [b.id, b])));
  const visibleChats = computed(() =>
    botFilter.value ? chats.value.filter((c) => c.bot_id === botFilter.value) : chats.value,
  );

  function showToast(text: string) {
    toast.value = { id: ++toastSeq, text };
  }

  function conv(chatId: number): Conversation {
    return conversations.value[chatId] ?? EMPTY;
  }

  function setConv(chatId: number, patch: Partial<Conversation>) {
    conversations.value = { ...conversations.value, [chatId]: { ...conv(chatId), ...patch } };
  }

  async function loadBots() {
    try {
      bots.value = await api.bots();
    } catch (e) {
      showToast(errorMessage(e));
    }
  }

  async function loadChats() {
    try {
      chats.value = await api.chats();
      chatsLoaded.value = true;
    } catch (e) {
      showToast(errorMessage(e));
    }
  }

  function scheduleChatsReload() {
    clearTimeout(reloadTimer);
    reloadTimer = setTimeout(() => void loadChats(), delay);
  }

  /** Loads the newest page; merges when it overlaps what we have, otherwise starts over. */
  async function refreshLatest(chatId: number) {
    if (conv(chatId).loading) return;
    setConv(chatId, { loading: true, error: '' });
    try {
      const page = await api.messages(chatId, 0, PAGE_SIZE);
      const c = conv(chatId);
      const newestKnown = c.items.length ? c.items[c.items.length - 1].id : 0;
      const overlaps = c.loaded && page.length > 0 && page[0].id <= newestKnown;
      if (overlaps) setConv(chatId, { items: mergeById(c.items, page), loading: false, loaded: true });
      else setConv(chatId, { items: page, hasMore: page.length >= PAGE_SIZE, loading: false, loaded: true });
    } catch (e) {
      setConv(chatId, { loading: false, error: errorMessage(e) });
    }
  }

  async function loadOlder(chatId: number) {
    const c = conv(chatId);
    if (!c.loaded || c.loading || !c.hasMore) return;
    setConv(chatId, { loading: true, error: '' });
    try {
      const page = await api.messages(chatId, c.items[0]?.id ?? 0, PAGE_SIZE);
      setConv(chatId, { items: mergeById(page, conv(chatId).items), hasMore: page.length >= PAGE_SIZE, loading: false });
    } catch (e) {
      setConv(chatId, { loading: false, error: errorMessage(e) });
    }
  }

  function chatOfMessage(messageId: number): number | undefined {
    for (const [id, c] of Object.entries(conversations.value)) {
      if (c.items.some((m) => m.id === messageId)) return Number(id);
    }
    return undefined;
  }

  function upsertMessage(m: Message) {
    const c = conversations.value[m.chat_id];
    if (!c?.loaded) return;
    const known = c.items.some((x) => x.id === m.id);
    const oldest = c.items[0]?.id ?? 0;
    // Never insert a message older than the loaded window: that would leave a hole.
    if (known || !c.hasMore || m.id > oldest) setConv(m.chat_id, { items: mergeById(c.items, [m]) });
  }

  function removeMessage(chatId: number, messageId: number) {
    const c = conversations.value[chatId];
    if (!c) return;
    setConv(chatId, { items: c.items.filter((m) => m.id !== messageId) });
  }

  async function refreshMessage(messageId: number, chatHint?: number) {
    try {
      upsertMessage(await api.message(messageId));
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) {
        const chatId = chatHint ?? chatOfMessage(messageId);
        if (chatId) removeMessage(chatId, messageId);
      }
    }
  }

  async function handleEvent(ev: ArchiveEvent) {
    switch (ev.type) {
      case 'message.created':
      case 'message.updated':
        scheduleChatsReload();
        if (conv(ev.data.chat_id).loaded) await refreshMessage(ev.data.message_id, ev.data.chat_id);
        return;
      case 'message.deleted':
        removeMessage(ev.data.chat_id, ev.data.message_id);
        scheduleChatsReload();
        return;
      case 'media.updated':
        for (const id of ev.data.message_ids ?? []) {
          if (chatOfMessage(id) !== undefined) await refreshMessage(id);
        }
        return;
      case 'bot.status': {
        const { bot_id, status, error } = ev.data;
        if (!botsById.value.has(bot_id)) {
          await loadBots();
          return;
        }
        bots.value = bots.value.map((b) => (b.id === bot_id ? { ...b, status, last_error: error } : b));
        return;
      }
    }
  }

  async function resync() {
    await Promise.all([loadBots(), loadChats()]);
    const loaded = Object.entries(conversations.value)
      .filter(([, c]) => c.loaded)
      .map(([id]) => Number(id));
    await Promise.all(loaded.map((id) => refreshLatest(id)));
  }

  /** Deletes an archived message; throws on failure so the caller can report it. */
  async function deleteMessage(m: Message) {
    try {
      await api.deleteMessage(m.id);
    } catch (e) {
      if (!(e instanceof ApiError && e.status === 404)) throw e;
    }
    removeMessage(m.chat_id, m.id);
    scheduleChatsReload();
  }

  async function retryMedia(m: Message, mediaId: number) {
    try {
      await api.retryMedia(mediaId);
      const c = conv(m.chat_id);
      setConv(m.chat_id, {
        items: c.items.map((x) =>
          x.media.some((md) => md.id === mediaId)
            ? { ...x, media: x.media.map((md) => (md.id === mediaId ? { ...md, state: 'pending' as const, error: '' } : md)) }
            : x,
        ),
      });
    } catch (e) {
      showToast(errorMessage(e));
      await refreshMessage(m.id, m.chat_id);
    }
  }

  return {
    api,
    bots,
    chats,
    chatsLoaded,
    botFilter,
    conversations,
    toast,
    viewer,
    sharedMediaOpen,
    botsById,
    visibleChats,
    showToast,
    conv,
    loadBots,
    loadChats,
    refreshLatest,
    loadOlder,
    refreshMessage,
    handleEvent,
    resync,
    deleteMessage,
    retryMedia,
  };
}

export type Store = ReturnType<typeof createStore>;

export const StoreContext = createContext<Store | null>(null);

export function useStore(): Store {
  const s = useContext(StoreContext);
  if (!s) throw new Error('StoreContext missing');
  return s;
}
