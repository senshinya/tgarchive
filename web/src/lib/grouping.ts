import type { Message } from '../api/types';
import { dayKey, formatDayLabel } from './format';

/** Consecutive messages further apart than this start a new visual group. */
export const GROUP_GAP_SECONDS = 600;

export type BubbleContent = { kind: 'message'; key: string; msg: Message } | { kind: 'album'; key: string; msgs: Message[] };

export type Bubble = BubbleContent & { first: boolean; last: boolean };

export type ListEntry =
  | { kind: 'date'; key: string; label: string }
  | { kind: 'group'; key: string; bubbles: Bubble[] };

function lastMsg(b: BubbleContent): Message {
  return b.kind === 'message' ? b.msg : b.msgs[b.msgs.length - 1];
}

/**
 * Splits an ascending message list into date separators and sender groups. Messages of one
 * media group that are adjacent become a single album bubble. In a bot's merged timeline each
 * chat is a different sender, so a change of chat also starts a new group; watched channel posts
 * are never grouped.
 */
export function groupMessages(messages: Message[], now: Date = new Date()): ListEntry[] {
  const sorted = [...messages].sort((a, b) => a.id - b.id);
  const out: ListEntry[] = [];
  let currentDay = '';
  let group: BubbleContent[] = [];

  const flush = () => {
    if (group.length === 0) return;
    const bubbles = group.map((b, i) => ({ ...b, first: i === 0, last: i === group.length - 1 }));
    out.push({ kind: 'group', key: `g${bubbles[0].key}`, bubbles });
    group = [];
  };

  for (const m of sorted) {
    const day = dayKey(m.date);
    if (day !== currentDay) {
      flush();
      currentDay = day;
      out.push({ kind: 'date', key: `d${day}`, label: formatDayLabel(m.date, now) });
    }
    const prev = group[group.length - 1];
    if (prev && m.media_group_id && (prev.kind === 'album' ? prev.msgs[0] : prev.msg).media_group_id === m.media_group_id) {
      if (prev.kind === 'album') prev.msgs.push(m);
      else group[group.length - 1] = { kind: 'album', key: prev.key, msgs: [prev.msg, m] };
      continue;
    }
    // Channel posts stand alone, as in a Telegram channel: each is its own group with its own tail.
    if (prev && (m.date - lastMsg(prev).date > GROUP_GAP_SECONDS || lastMsg(prev).chat_id !== m.chat_id || m.source === 'channel_watch')) flush();
    group.push({ kind: 'message', key: String(m.id), msg: m });
  }
  flush();
  return out;
}
