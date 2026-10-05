import { describe, expect, it } from 'vitest';
import type { Message } from '../api/types';
import { groupMessages, type ListEntry } from './grouping';
import { makeMessage } from '../test/fixtures';

const at = (d: number, h: number, m = 0) => new Date(2026, 9, d, h, m).getTime() / 1000;

function msg(id: number, date: number, extra: Partial<Message> = {}): Message {
  return {
    id,
    chat_id: 1,
    tg_message_id: id,
    source: 'bot_update',
    media_group_id: '',
    date,
    edit_date: 0,
    kind: 'text',
    text: `m${id}`,
    entities: [],
    reply_to_tg_message_id: 0,
    origin_chat_title: '',
    origin_link: '',
    media: [],
    ...extra,
  };
}

function shape(entries: ListEntry[]): string[] {
  return entries.map((e) =>
    e.kind === 'date'
      ? `[${e.label}]`
      : e.bubbles
          .map((b) => {
            const ids = b.kind === 'album' ? `{${b.msgs.map((m) => m.id).join(',')}}` : String(b.msg.id);
            return `${ids}${b.first ? 'F' : ''}${b.last ? 'L' : ''}`;
          })
          .join(' '),
  );
}

const now = new Date(2026, 9, 4, 20, 0);

describe('groupMessages', () => {
  it('inserts date separators and marks first/last bubbles', () => {
    const list = [msg(1, at(3, 10)), msg(2, at(3, 10, 1)), msg(3, at(4, 9)), msg(4, at(4, 9, 2)), msg(5, at(4, 9, 3))];
    expect(shape(groupMessages(list, now))).toEqual(['[昨天]', '1F 2L', '[今天]', '3F 4 5L']);
  });

  it('breaks groups on gaps longer than ten minutes', () => {
    const list = [msg(1, at(4, 9)), msg(2, at(4, 9, 5)), msg(3, at(4, 9, 16))];
    expect(shape(groupMessages(list, now))).toEqual(['[今天]', '1F 2L', '3FL']);
  });

  it('collects adjacent media-group messages into one album bubble', () => {
    const list = [
      msg(1, at(4, 9)),
      msg(2, at(4, 9), { media_group_id: 'g', kind: 'photo' }),
      msg(3, at(4, 9), { media_group_id: 'g', kind: 'photo' }),
      msg(4, at(4, 9), { media_group_id: 'g', kind: 'video' }),
      msg(5, at(4, 9), { media_group_id: 'h', kind: 'photo' }),
      msg(6, at(4, 9, 1)),
    ];
    // A media group of one stays a plain message bubble.
    expect(shape(groupMessages(list, now))).toEqual(['[今天]', '1F {2,3,4} 5 6L']);
  });

  it('sorts by id regardless of input order', () => {
    const list = [msg(2, at(4, 9, 1)), msg(1, at(4, 9))];
    expect(shape(groupMessages(list, now))).toEqual(['[今天]', '1F 2L']);
  });

  it('returns nothing for an empty chat', () => {
    expect(groupMessages([], now)).toEqual([]);
  });

  it('starts a new group when the chat (sender) changes', () => {
    const base = 1_790_000_000;
    const msgs = [
      makeMessage({ id: 1, chat_id: 10, date: base }),
      makeMessage({ id: 2, chat_id: 11, date: base + 1 }),
      makeMessage({ id: 3, chat_id: 11, date: base + 2 }),
      makeMessage({ id: 4, chat_id: 10, date: base + 3 }),
    ];
    const groups = groupMessages(msgs).filter((e) => e.kind === 'group');
    expect(groups.map((g) => (g.kind === 'group' ? g.bubbles.length : 0))).toEqual([1, 2, 1]);
  });
});
