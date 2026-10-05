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

export interface Chat {
  id: number;
  bot_id: number;
  sender: Sender;
  last_message_at: number;
  last_kind: string;
  last_text: string;
}

export type MediaState = 'pending' | 'done' | 'failed' | 'too_large';

export interface Media {
  id: number;
  role: 'main' | 'thumb';
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
  source: 'bot_update' | 'userbot_fetch';
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

export type ArchiveEvent =
  | { type: 'message.created' | 'message.updated' | 'message.deleted'; data: MessageRef }
  | { type: 'media.updated'; data: { media_id: number; message_ids: number[] | null } }
  | { type: 'bot.status'; data: { bot_id: number; status: string; error: string } };

export const EVENT_TYPES = [
  'message.created',
  'message.updated',
  'message.deleted',
  'media.updated',
  'bot.status',
] as const;
