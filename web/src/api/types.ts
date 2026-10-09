// JSON shapes served by internal/httpapi. Field names match the Go json tags exactly.

export interface Bot {
  id: number;
  tg_bot_id: number;
  username: string;
  name: string;
  has_avatar: boolean;
  enabled: boolean;
  status: string; // running / error / stopped / removed
  last_error: string;
}

export interface Sender {
  tg_user_id: number;
  first_name: string;
  last_name: string;
  username: string;
  has_avatar: boolean;
}

/** A watched channel as chats and watches carry it. */
export interface ChannelRef {
  channel_id: number;
  title: string;
  username: string;
  has_avatar: boolean;
}

/** What the chat list knows about a channel conversation's watch. */
export interface WatchBrief {
  id: number;
  enabled: boolean;
  status: 'ok' | 'error';
  error: string;
  window_minutes: number;
  pending: number;
  hits: number;
}

export interface Chat {
  id: number;
  /** private: a bot × sender chat; channel: a watched channel (bot_id 0, empty sender). */
  kind: 'private' | 'channel';
  bot_id: number;
  sender: Sender;
  channel: ChannelRef | null;
  /** The channel's watch; null for private chats and for channels no longer watched. */
  watch: WatchBrief | null;
  last_message_at: number;
  last_kind: string;
  last_text: string;
  /** The newest message read (channels; see markRead). */
  last_read_id: number;
  /** Messages newer than last_read_id; always 0 for private chats. */
  unread: number;
}

export type MediaState = 'pending' | 'done' | 'failed' | 'too_large';

export interface Media {
  id: number;
  role: 'main' | 'thumb' | 'link_preview';
  kind: string; // photo / video / animation / voice / audio / document / sticker / video_note
  mime: string;
  file_name: string;
  size: number;
  width: number;
  height: number;
  duration: number;
  waveform?: string; // base64 of the 5-bit packed Telegram waveform
  state: MediaState;
  error: string;
  /** The original's video codec when the server keeps an H.264 copy for browsers that cannot
   * play it (served at `?compat=1`). */
  compat_codec?: string;
}

export interface Entity {
  type: string;
  offset: number; // UTF-16 code units
  length: number;
  url?: string;
  language?: string;
  user_id?: number;
  custom_emoji_id?: string;
}

export interface ForwardOrigin {
  type: 'user' | 'hidden_user' | 'chat' | 'channel';
  name: string;
  username?: string;
  user_id?: number;
  chat_id?: number;
  message_id?: number;
  date: number;
  signature?: string;
}

export interface ReplyRef {
  id: number;
  kind: string;
  text: string;
  /** The quoted comment's author (extra.from); absent for bot chats. */
  extra?: { from?: Commenter };
}

/** Who wrote a comment: a user, or a channel or group writing as itself. */
export interface Commenter {
  kind: 'user' | 'channel';
  id: number;
  name: string;
  /** A profile photo is known (served at /avatars/users|channels/:id). */
  photo?: boolean;
}

/** What an archived post's stats say about its comments; present when it takes comments. */
export interface CommentsInfo {
  /** The authors of the newest kept comments, newest first (at most 3). */
  recent: Commenter[];
  /** The discussion group cannot be read by the account: the comments are not kept. */
  unreadable?: boolean;
}

export type MessageKind =
  | 'text'
  | 'photo'
  | 'video'
  | 'animation'
  | 'voice'
  | 'audio'
  | 'document'
  | 'sticker'
  | 'video_note'
  | 'location'
  | 'venue'
  | 'contact'
  | 'poll'
  | 'dice'
  | 'other';

export interface Message {
  id: number;
  chat_id: number;
  tg_message_id: number;
  source: 'bot_update' | 'userbot_fetch' | 'channel_watch' | 'channel_comment';
  media_group_id: string;
  date: number;
  edit_date: number;
  kind: MessageKind;
  text: string;
  entities: Entity[];
  forward_origin?: ForwardOrigin;
  reply_to_tg_message_id: number;
  reply?: ReplyRef;
  origin_chat_title: string;
  origin_link: string;
  extra?: Record<string, unknown>;
  media: Media[];
  /** Present when the message is a Telegraph link with an archiving job. */
  article?: ArticleSummary;
  /** Counters when a watched channel post was archived (channel_watch only). */
  stats?: PostStats;
  /** Set when the message is a favorite. */
  favorite?: FavoriteInfo | null;
  /** The archived post a comment belongs to; 0 for anything but a comment. */
  thread_root_id?: number;
}

export interface Tag {
  id: number;
  name: string;
}

export interface TagCount extends Tag {
  /** How many favorites carry it. */
  count: number;
}

export interface FavoriteInfo {
  /** When it was added to the favorites. */
  at: number;
  tags: Tag[];
}

export interface FavoritesPage {
  items: { fav_id: number; message: Message }[];
  /** The `before` cursor of the next page; 0 when this was the last. */
  next: number;
}

/** One reaction counter; key is the emoji, "custom:<id>" or "paid". */
export interface ReactionStat {
  key: string;
  emoji?: string;
  custom_id?: string;
  /** The custom emoji's sticker media and its type (tgs / webm / webp). */
  media_id?: number;
  mime?: string;
  count: number;
}

export interface PostStats {
  reactions: ReactionStat[];
  total: number;
  views: number;
  forwards: number;
  replies: number;
  hit?: { at: number; reasons: string[] };
  /** Present when the post takes comments; the count is `replies`. */
  comments?: CommentsInfo;
}

/** A channel as the watch picker lists it. */
export interface ChannelInfo {
  channel_id: number;
  title: string;
  username: string;
  participants: number;
  watched: boolean;
}

/** GET /api/admin/channels: the joined channels found so far by the server's background scan. */
export interface ChannelList {
  channels: ChannelInfo[];
  /** A scan is running; channels may be partial. */
  loading: boolean;
  /** When the last complete scan finished (unix seconds); 0 for never. */
  updated_at: number;
  /** Why the last scan stopped early. */
  error: string;
}

export type CondCmp = 'gte' | 'lte';
export type CondLeaf =
  | { metric: 'reaction'; key: string; cmp: CondCmp; value: number }
  | { metric: 'total' | 'views' | 'forwards' | 'replies'; cmp: CondCmp; value: number }
  | { metric: 'ratio'; num: string; den: 'total' | 'views'; cmp: CondCmp; value: number }
  | { metric: 'type'; cmp: 'is' | 'not'; value: 'photo' | 'video' | 'file' | 'text' }
  | { metric: 'text'; cmp: 'contains' | 'not_contains'; value: string };
export interface CondGroup {
  op: 'and' | 'or';
  items: CondNode[];
}
export type CondNode = CondGroup | CondLeaf;

export interface Watch {
  id: number;
  channel: ChannelRef;
  chat_id: number;
  window_minutes: number;
  cond: CondGroup | null;
  enabled: boolean;
  status: 'ok' | 'error';
  error: string;
  pending: number;
  hits: number;
  created_at: number;
  /** Last change of settings or status (enabling it included). */
  updated_at: number;
  /** When a poll last finished; 0 when none has. */
  last_polled_at: number;
  /** Archived posts (an album counts once) published in the last 24 hours / 7 days. */
  hits_24h: number;
  hits_7d: number;
  /** The newest archived post's date; 0 when none. */
  last_hit_at: number;
  /** The poll interval now in force, against which a stalled watch is judged. */
  poll_seconds: number;
  /** The latest manual backfill; null when none ran since the server started. */
  backfill: BackfillState | null;
}

export interface BackfillState {
  running: boolean;
  hours: number;
  /** Posts looked at so far. */
  scanned: number;
  /** Newly archived posts (an album counts once). */
  archived: number;
  error: string;
  started_at: number;
  finished_at: number;
}

export interface WatchInput {
  channel_id: number;
  window_minutes: number;
  cond: CondGroup;
  enabled: boolean;
}

export interface TestPost {
  tg_message_id: number;
  date: number;
  kind: string;
  text: string;
  stats: PostStats;
  hit: boolean;
  reasons: string[];
}

export interface WatchTestResult {
  posts: TestPost[];
  reactions_available: { all: boolean; list: ReactionStat[] };
}

export type ArticleState = 'queued' | 'fetching' | 'fetched' | 'failed';

/** Card data for a Telegraph link message (GET /api/messages/:id carries it). */
export interface ArticleSummary {
  state: ArticleState;
  error: string;
  title: string;
  description: string;
  author_name: string;
  /** Cover image; only set once it has been downloaded. */
  image_media_id?: number;
  url: string;
}

/** Normalized Telegraph node: text, or an element. img/video carry data-media-id + data-src;
 * iframes arrive as {tag: 'embed', attrs: {href, src}}. */
export type ArticleNode = string | ArticleElement;

export interface ArticleElement {
  tag: string;
  attrs?: Record<string, string>;
  children?: ArticleNode[];
}

export interface ArticleMedia {
  id: number;
  kind: string; // photo / video
  state: MediaState;
  width: number;
  height: number;
  duration: number;
  mime: string;
}

/** GET /api/messages/:id/article */
export interface Article {
  url: string;
  title: string;
  description: string;
  author_name: string;
  author_url: string;
  views: number;
  fetched_at: number;
  content: ArticleNode[];
  media: ArticleMedia[];
}

export type SharedMediaType = 'media' | 'file' | 'link';

/** The media wall's filters: what kind of media, and from which kind of chat. */
export type WallType = 'all' | 'photo' | 'video';
export type WallSource = 'all' | 'private' | 'channel';

export interface DayCount {
  day: string; // YYYY-MM-DD
  count: number;
}

export interface Stats {
  totals: {
    messages: number;
    private_chats: number;
    channel_chats: number;
    media_files: number;
    media_bytes: number;
    db_bytes: number;
    disk_free: number;
    disk_total: number;
  };
  /** Messages per day over the last 53 weeks; days without any are left out. */
  daily: DayCount[];
  /** Per month, oldest first: new messages and newly archived media bytes. */
  monthly: { month: string; messages: number; media_bytes: number }[];
  top_chats: { chat_id: number; messages: number; media_bytes: number }[];
  /** By main media kind ('other' for thumbnails, link previews…), most first. */
  media_kinds: { kind: string; count: number; bytes: number }[];
  media_states: { done: number; pending: number; failed: number; too_large: number };
  watches: { watch_id: number; chat_id: number; title: string; daily: DayCount[]; hits: number; scanned: number; scan_hits: number }[];
}

export interface WhitelistEntry {
  tg_user_id: number;
  note: string;
  can_fetch: boolean;
}

export interface RejectedSender {
  tg_user_id: number;
  first_name: string;
  username: string;
  last_seen_at: number;
  count: number;
}

export interface AddBotStep {
  step: string; // getMe / logOut / start
  ok: boolean;
  detail: string;
}

export interface AddBotResult {
  bot_id?: number;
  error?: string;
  steps: AddBotStep[];
}

export interface TelegramApp {
  configured: boolean;
  api_id: number;
  server: { managed: boolean; state: string; error: string };
}

export type UserbotState =
  | 'unconfigured'
  | 'connecting'
  | 'logged_out'
  | 'code_sent'
  | 'password_needed'
  | 'ready'
  | 'error';

export interface UserbotInfo {
  state: UserbotState;
  phone: string;
  name: string;
  tg_user_id: number;
  error: string;
}

export interface MessageRef {
  chat_id: number;
  message_id: number;
}

/** Byte progress of one download in flight; total is 0 while unknown. */
export interface DownloadProgress {
  media_id: number;
  done: number;
  total: number;
  started_at: number;
}

/** A media row in the downloads panel, with the newest message using it. */
export interface DownloadItem {
  media_id: number;
  message_id: number;
  chat_id: number;
  kind: string;
  file_name: string;
  size: number;
  error?: string;
}

export interface ActiveDownload extends DownloadItem {
  done: number;
  total: number;
  started_at: number;
}

export interface Downloads {
  active: ActiveDownload[];
  queued: { count: number; bytes: number };
  failed: DownloadItem[];
  /** Bytes per second over the last few seconds. */
  speed: number;
}

export type ArchiveEvent =
  | { type: 'message.created' | 'message.updated' | 'message.deleted'; data: MessageRef }
  | { type: 'media.updated'; data: { media_id: number; message_ids: number[] | null } }
  | { type: 'bot.status'; data: { bot_id: number; status: string; error: string } }
  | { type: 'download.progress'; data: { items: DownloadProgress[]; speed: number } }
  | { type: 'watch.updated'; data: { watch_id: number } }
  | { type: 'favorites.updated'; data: null }
  | { type: 'chat.read'; data: { chat_id: number } }
  /** New or changed comments of an archived post. */
  | { type: 'comments.updated'; data: { chat_id: number; root_id: number } }
  /** Synthetic, never sent by the server: the store broadcasts it to `onEvent` listeners after it
   * resynced following a reconnect, so views holding their own fetched data refetch it. */
  | { type: 'resync'; data: null };

export const EVENT_TYPES = [
  'message.created',
  'message.updated',
  'message.deleted',
  'media.updated',
  'bot.status',
  'download.progress',
  'watch.updated',
  'favorites.updated',
  'chat.read',
  'comments.updated',
] as const;

/** One search result: a message and the text around its first match. */
export interface SearchHit {
  message: Message;
  /** Where the snippet comes from: the text or caption, a file name, or the archived article. */
  field: 'body' | 'files' | 'article';
  snippet: string;
  /** Matches within the snippet as [start, length] in UTF-16 units. */
  ranges: [number, number][];
}

export interface SearchPage {
  items: SearchHit[];
  /** The `before` cursor of the next page; 0 when this was the last. */
  next: number;
}
