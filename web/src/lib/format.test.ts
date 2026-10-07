import { describe, expect, it } from 'vitest';
import {
  botName,
  dayKey,
  fileColor,
  fileExtension,
  formatDayLabel,
  formatDuration,
  formatFullDate,
  formatAgo,
  formatListTime,
  formatMonth,
  formatSize,
  formatTime,
  hashString,
  initials,
  kindLabel,
  monthKey,
  peerColorIndex,
  previewText,
  senderName,
} from './format';

// Build timestamps from local wall-clock parts so the tests pass in any TZ.
const at = (y: number, mo: number, d: number, h = 12, mi = 0) => new Date(y, mo - 1, d, h, mi).getTime() / 1000;
const now = new Date(2026, 9, 4, 15, 30); // 2026-10-04 15:30 local (a Sunday)

describe('dates', () => {
  it('formats time with zero padding', () => {
    expect(formatTime(at(2026, 10, 4, 9, 5))).toBe('09:05');
  });

  it('labels date separators relative to today', () => {
    expect(formatDayLabel(at(2026, 10, 4, 0, 1), now)).toBe('今天');
    expect(formatDayLabel(at(2026, 10, 3, 23, 59), now)).toBe('昨天');
    expect(formatDayLabel(at(2026, 9, 1), now)).toBe('9月1日');
    expect(formatDayLabel(at(2025, 12, 31), now)).toBe('2025年12月31日');
  });

  it('formats the chat list time column', () => {
    expect(formatListTime(at(2026, 10, 4, 8, 7), now)).toBe('08:07');
    expect(formatListTime(at(2026, 9, 28), now)).toBe('周一');
    expect(formatListTime(at(2026, 9, 27), now)).toBe('9/27');
    expect(formatListTime(at(2024, 2, 29), now)).toBe('2024/2/29');
    expect(formatListTime(0, now)).toBe('');
  });

  it('builds day and month keys and labels', () => {
    expect(dayKey(at(2026, 1, 2))).toBe('2026-01-02');
    expect(monthKey(at(2026, 1, 2))).toBe('2026-01');
    expect(formatMonth(at(2026, 1, 2))).toBe('2026年1月');
    expect(formatFullDate(at(2026, 10, 4, 13, 5))).toBe('2026年10月4日 13:05');
  });
});

describe('sizes and durations', () => {
  it('formats byte sizes like Telegram', () => {
    expect(formatSize(0)).toBe('0 B');
    expect(formatSize(512)).toBe('512 B');
    expect(formatSize(1536)).toBe('1.5 KB');
    expect(formatSize(20 * 1024 * 1024)).toBe('20 MB');
    expect(formatSize(1.25 * 1024 ** 3)).toBe('1.3 GB');
  });

  it('formats durations', () => {
    expect(formatDuration(7)).toBe('0:07');
    expect(formatDuration(754)).toBe('12:34');
    expect(formatDuration(3723)).toBe('1:02:03');
    expect(formatDuration(-3)).toBe('0:00');
  });
});

describe('names and labels', () => {
  it('derives sender and bot names with fallbacks', () => {
    expect(senderName({ first_name: 'Alice', last_name: 'Liddell', username: 'al', tg_user_id: 1 })).toBe('Alice Liddell');
    expect(senderName({ first_name: '', last_name: '', username: 'al', tg_user_id: 1 })).toBe('@al');
    expect(senderName({ first_name: '', last_name: '', username: '', tg_user_id: 9 })).toBe('用户 9');
    expect(botName({ name: '', username: 'archive_bot', id: 1 })).toBe('@archive_bot');
  });

  it('takes initials from up to two words, emoji-safe', () => {
    expect(initials('alice liddell')).toBe('AL');
    expect(initials('张三')).toBe('张');
    expect(initials('😀 Smile')).toBe('😀S');
    expect(initials('@bot')).toBe('B');
  });

  it('labels kinds and previews', () => {
    expect(kindLabel('photo')).toBe('照片');
    expect(kindLabel('mystery')).toBe('不支持的消息');
    expect(previewText('photo', '')).toBe('照片');
    expect(previewText('photo', '  hi\n there ')).toBe('hi there');
  });

  it('hashes names to stable non-negative numbers', () => {
    expect(hashString('频道')).toBe(hashString('频道'));
    expect(hashString('a')).not.toBe(hashString('b'));
    expect(hashString('x'.repeat(500))).toBeGreaterThanOrEqual(0);
  });

  it('picks stable peer colors', () => {
    expect(peerColorIndex(42)).toBe(0);
    expect(peerColorIndex(-1001234567890)).toBe(peerColorIndex(1001234567890));
  });

  it('derives file extension and tile color', () => {
    expect(fileExtension('Report.PDF', 'application/pdf')).toBe('pdf');
    expect(fileExtension('', 'application/x-zip')).toBe('zip');
    expect(fileColor('pdf')).toBe('var(--color-error)');
    expect(fileColor('zip')).toBe('var(--color-warning)');
    expect(fileColor('xlsx')).toBe('var(--color-text-green)');
    expect(fileColor('txt')).toBe('var(--color-primary)');
  });
});

describe('formatAgo', () => {
  it('says how long ago, coarsely', () => {
    const now = 1_790_000_000;
    expect(formatAgo(now - 30, now)).toBe('刚刚');
    expect(formatAgo(now - 60, now)).toBe('1 分钟前');
    expect(formatAgo(now - 59 * 60, now)).toBe('59 分钟前');
    expect(formatAgo(now - 3600, now)).toBe('1 小时前');
    expect(formatAgo(now - 86400 * 2 - 5, now)).toBe('2 天前');
    expect(formatAgo(now + 100, now)).toBe('刚刚');
  });
});
