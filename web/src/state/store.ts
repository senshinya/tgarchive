import { computed, signal } from '@preact/signals';
import { createContext } from 'preact';
import { useContext } from 'preact/hooks';
import { ApiError, PAGE_SIZE, convMessages, errorMessage, type Api } from '../api/client';
import type { ArchiveEvent, Bot, Chat, Downloads, Entity, Message } from '../api/types';

export interface Conversation {
  items: Message[]; // ascending by id
  hasMore: boolean;
  loading: boolean;
  loaded: boolean;
  error: string;
}

const EMPTY: Conversation = { items: [], hasMore: true, loading: false, loaded: false, error: '' };

/** One photo/video/GIF the media viewer can show. */
export interface ViewerItem {
  mediaId: number;
  kind: string; // photo / video / animation
  date: number;
  text: string; // caption, '' for none
  entities: Entity[];
  /** The chat it was sent in, for the viewer's sender title; absent for article media. */
  chatId?: number;
}

/** Opens the viewer on a chat's media (walking the whole chat), or on an explicit list such as
 * the media of one archived article (walking only that list). */
export type ViewerTarget =
  // chatId is a conversation key: a chat id, or -botId to walk a bot's merged timeline.
  | { chatId: number; messageId: number; mediaId: number }
  | { list: ViewerItem[]; mediaId: number; title: string };

export interface Toast {
  id: number;
  text: string;
}

/** How the left column lists conversations: one per bot × sender, or one merged timeline per bot. */
export type ListMode = 'people' | 'bot';
export const LIST_MODE_KEY = 'tgarchive.listMode';

function readListMode(): ListMode {
  try {
    return localStorage.getItem(LIST_MODE_KEY) === 'bot' ? 'bot' : 'people';
  } catch {
    return 'people';
  }
}

/** One row of the left column in bot mode: a bot's merged timeline. */
export interface BotEntry {
  bot: Bot;
  /** The bot's most recently active chat, for the preview line; undefined when it has none. */
  last?: Chat;
  senders: number;
}

function idsKey(ids: number[]): string {
  return [...ids].sort((a, b) => a - b).join(',');
}

function mergeById(a: Message[], b: Message[]): Message[] {
  const map = new Map<number, Message>();
  for (const m of a) map.set(m.id, m);
  for (const m of b) map.set(m.id, m);
  return [...map.values()].sort((x, y) => x.id - y.id);
}

/** Byte progress of one download in flight (total 0 while unknown). */
export interface MediaProgress {
  done: number;
  total: number;
}

/** What the left header's downloads button shows. */
export interface DownloadSummary {
  active: number;
  /** Fraction done over the active downloads with a known size; undefined when none has one. */
  fraction?: number;
  queued: number;
  failed: number;
}

export function createStore(api: Api, opts: { chatsReloadDelay?: number; downloadsReloadDelay?: number } = {}) {
  const delay = opts.chatsReloadDelay ?? 300;
  const downloadsDelay = opts.downloadsReloadDelay ?? 1000;
  const bots = signal<Bot[]>([]);
  const chats = signal<Chat[]>([]);
  const chatsLoaded = signal(false);
  const botFilter = signal(0); // 0 = 全部
  const conversations = signal<Record<number, Conversation>>({});
  const toast = signal<Toast | null>(null);
  const viewer = signal<ViewerTarget | null>(null); // media viewer target
  const sharedMediaOpen = signal(false); // right column
  const listMode = signal<ListMode>(readListMode());
  const progress = signal<Map<number, MediaProgress>>(new Map()); // media id → bytes, from download.progress
  const downloads = signal<Downloads | null>(null); // the downloads panel snapshot
  /** A message for conversation `key` to scroll to, loading older pages until it shows it (set by
   * the downloads panel). */
  const jumpTo = signal<{ key: number; messageId: number } | null>(null);
  let downloadsTimer: ReturnType<typeof setTimeout> | undefined;
  let activeKey = ''; // the set of media ids in the last progress event (only events set it)
  let toastSeq = 0;
  let reloadTimer: ReturnType<typeof setTimeout> | undefined;
  const eventListeners = new Set<(ev: ArchiveEvent) => void>();

  const botsById = computed(() => new Map(bots.value.map((b) => [b.id, b])));
  // Falls back to "全部" when the selected bot was purged from `bots` but the reload that
  // should reset `botFilter` to 0 hasn't landed yet (or a future consumer forgets to reset it).
  const effectiveBotFilter = computed(() => (botFilter.value && botsById.value.has(botFilter.value) ? botFilter.value : 0));
  const visibleChats = computed(() =>
    effectiveBotFilter.value ? chats.value.filter((c) => c.bot_id === effectiveBotFilter.value) : chats.value,
  );

  // Bots with archived chats, most recently active first, then bots still running but empty.
  const botEntries = computed<BotEntry[]>(() => {
    const out: BotEntry[] = [];
    for (const bot of bots.value) {
      const own = chats.value.filter((c) => c.bot_id === bot.id);
      if (own.length === 0 && bot.status === 'removed') continue;
      const last = own.reduce<Chat | undefined>((a, c) => (!a || c.last_message_at > a.last_message_at ? c : a), undefined);
      out.push({ bot, last, senders: own.length });
    }
    return out.sort((a, b) => (b.last?.last_message_at ?? -1) - (a.last?.last_message_at ?? -1));
  });

  const downloadSummary = computed<DownloadSummary>(() => {
    let done = 0;
    let total = 0;
    for (const p of progress.value.values()) {
      if (p.total > 0) {
        done += Math.min(p.done, p.total);
        total += p.total;
      }
    }
    return {
      active: progress.value.size,
      fraction: total > 0 ? done / total : undefined,
      queued: downloads.value?.queued.count ?? 0,
      failed: downloads.value?.failed.length ?? 0,
    };
  });

  async function loadDownloads() {
    try {
      const snap = await api.downloads();
      downloads.value = snap;
      // The snapshot is newer than any progress event a frozen or reconnected page missed
      // (including the final empty one), so it replaces the live progress outright.
      progress.value = new Map(snap.active.map((a) => [a.media_id, { done: a.done, total: a.total }]));
      // activeKey is left alone: it tracks what the events said, and the snapshot may never
      // list some of it (a download whose message was deleted), which must not cause a reload
      // on every event.
    } catch {
      // Keep the last snapshot; the next event or resync tries again.
    }
  }

  // Throttled, not debounced: events keep arriving every second, and resetting the timer on
  // each could postpone the reload forever.
  function scheduleDownloadsReload() {
    if (downloadsTimer !== undefined) return;
    downloadsTimer = setTimeout(() => {
      downloadsTimer = undefined;
      void loadDownloads();
    }, downloadsDelay);
  }

  function setListMode(mode: ListMode) {
    listMode.value = mode;
    try {
      localStorage.setItem(LIST_MODE_KEY, mode);
    } catch {
      // Not persisted (private mode, blocked storage): the choice lasts for this page only.
    }
  }

  /** The merged-timeline key of the bot that owns chat `chatId`; 0 while the chat is unknown. */
  function botKeyOf(chatId: number): number {
    const chat = chats.value.find((c) => c.id === chatId);
    return chat ? -chat.bot_id : 0;
  }

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
      if (botFilter.value && !botsById.value.has(botFilter.value)) botFilter.value = 0;
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
      const page = await convMessages(api, chatId, 0, PAGE_SIZE);
      const c = conv(chatId);
      const newestKnown = c.items.length ? c.items[c.items.length - 1].id : 0;
      const overlaps = c.loaded && page.length > 0 && page[0].id <= newestKnown;
      if (overlaps) {
        // Within the refreshed window [page[0].id, newest], the server is authoritative: drop
        // anything we had there that it no longer returns (deleted while disconnected). Items
        // older than the window are untouched.
        const pageIds = new Set(page.map((m) => m.id));
        const kept = c.items.filter((m) => m.id < page[0].id || pageIds.has(m.id));
        setConv(chatId, { items: mergeById(kept, page), loading: false, loaded: true });
      } else {
        setConv(chatId, { items: page, hasMore: page.length >= PAGE_SIZE, loading: false, loaded: true });
      }
    } catch (e) {
      setConv(chatId, { loading: false, error: errorMessage(e) });
    }
  }

  async function loadOlder(chatId: number) {
    const c = conv(chatId);
    if (!c.loaded || c.loading || !c.hasMore) return;
    setConv(chatId, { loading: true, error: '' });
    try {
      const page = await convMessages(api, chatId, c.items[0]?.id ?? 0, PAGE_SIZE);
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

  /** Adds or replaces `m` in its chat and in its bot's merged timeline, wherever loaded. */
  function upsertMessage(m: Message) {
    for (const key of [m.chat_id, botKeyOf(m.chat_id)]) {
      const c = conversations.value[key];
      if (!key || !c?.loaded) continue;
      const known = c.items.some((x) => x.id === m.id);
      const oldest = c.items[0]?.id ?? 0;
      // Never insert a message older than the loaded window: that would leave a hole.
      if (known || !c.hasMore || m.id > oldest) setConv(key, { items: mergeById(c.items, [m]) });
    }
  }

  /** Drops message `messageId` from every conversation holding it (its chat and its bot timeline). */
  function removeMessage(_chatId: number, messageId: number) {
    for (const [key, c] of Object.entries(conversations.value)) {
      if (c.items.some((m) => m.id === messageId)) setConv(Number(key), { items: c.items.filter((m) => m.id !== messageId) });
    }
  }

  /** Marks media `mediaId` pending in every loaded message using it (media are deduplicated). */
  function markMediaPending(mediaId: number) {
    const uses = (m: Message) => m.media.some((md) => md.id === mediaId);
    for (const [key, c] of Object.entries(conversations.value)) {
      if (!c.items.some(uses)) continue;
      setConv(Number(key), {
        items: c.items.map((m) =>
          uses(m) ? { ...m, media: m.media.map((md) => (md.id === mediaId ? { ...md, state: 'pending' as const, error: '' } : md)) } : m,
        ),
      });
    }
  }

  async function refreshMessage(messageId: number, chatHint?: number) {
    try {
      upsertMessage(await api.message(messageId));
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) {
        const chatId = chatHint ?? chatOfMessage(messageId);
        if (chatId) removeMessage(chatId, messageId);
      } else {
        console.error('refreshMessage failed', messageId, e);
      }
    }
  }

  /** Subscribes to every SSE event as handleEvent receives it, regardless of whether handleEvent
   * itself acts on it (e.g. it only refreshes a message that belongs to an already-loaded
   * conversation). For components that need to react to an event concerning one specific id that
   * may not be part of any loaded conversation — e.g. a deep-linked article reader. Returns an
   * unsubscribe function. */
  function onEvent(listener: (ev: ArchiveEvent) => void): () => void {
    eventListeners.add(listener);
    return () => {
      eventListeners.delete(listener);
    };
  }

  async function handleEvent(ev: ArchiveEvent) {
    for (const l of eventListeners) l(ev);
    switch (ev.type) {
      case 'message.created':
      case 'message.updated': {
        const { chat_id, message_id } = ev.data;
        scheduleChatsReload();
        let botKey = botKeyOf(chat_id);
        // A new sender's first message lands in a chat we have never listed: learn its bot
        // before deciding whether an open bot timeline wants it.
        if (!botKey && Object.entries(conversations.value).some(([k, c]) => Number(k) < 0 && c.loaded)) {
          await loadChats();
          botKey = botKeyOf(chat_id);
        }
        if (conv(chat_id).loaded || (botKey && conv(botKey).loaded)) await refreshMessage(message_id, chat_id);
        return;
      }
      case 'message.deleted':
        removeMessage(ev.data.chat_id, ev.data.message_id);
        scheduleChatsReload();
        return;
      case 'media.updated':
        scheduleDownloadsReload();
        for (const id of ev.data.message_ids ?? []) {
          if (chatOfMessage(id) !== undefined) await refreshMessage(id);
        }
        return;
      case 'download.progress': {
        const next = new Map(ev.data.items.map((p) => [p.media_id, { done: p.done, total: p.total }]));
        progress.value = next;
        // The snapshot names what is downloading; refresh it when that set changes.
        const key = idsKey([...next.keys()]);
        if (key !== activeKey) {
          activeKey = key;
          scheduleDownloadsReload();
        }
        if (downloads.value) downloads.value = { ...downloads.value, speed: ev.data.speed };
        return;
      }
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
    await Promise.all([loadBots(), loadChats(), loadDownloads()]);
    const loaded = Object.entries(conversations.value)
      .filter(([, c]) => c.loaded)
      .map(([id]) => Number(id));
    await Promise.all(loaded.map((id) => refreshLatest(id)));
    for (const l of eventListeners) l({ type: 'resync', data: null });
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
      markMediaPending(mediaId);
      scheduleDownloadsReload();
    } catch (e) {
      showToast(errorMessage(e));
      await refreshMessage(m.id, m.chat_id);
    }
  }

  /** Retries a failed media from the downloads panel, where no message object is at hand. */
  async function retryDownload(mediaId: number) {
    try {
      await api.retryMedia(mediaId);
      markMediaPending(mediaId);
    } catch (e) {
      showToast(errorMessage(e));
    }
    await loadDownloads();
  }

  return {
    api,
    progress,
    downloads,
    downloadSummary,
    jumpTo,
    loadDownloads,
    retryDownload,
    bots,
    chats,
    chatsLoaded,
    botFilter,
    effectiveBotFilter,
    conversations,
    toast,
    viewer,
    sharedMediaOpen,
    listMode,
    setListMode,
    botEntries,
    botKeyOf,
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
    onEvent,
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
