import { fireEvent, render, screen } from '@testing-library/preact';
import { describe, expect, it } from 'vitest';
import type { Entity } from '../../api/types';
import { RichText } from './RichText';

const e = (type: string, offset: number, length: number, extra: Partial<Entity> = {}): Entity => ({ type, offset, length, ...extra });

function html(text: string, entities: Entity[]) {
  const { container } = render(<RichText text={text} entities={entities} />);
  return container;
}

describe('RichText', () => {
  it('renders the formatting entities', () => {
    const c = html('b i u s c', [e('bold', 0, 1), e('italic', 2, 1), e('underline', 4, 1), e('strikethrough', 6, 1), e('code', 8, 1)]);
    expect(c.querySelector('strong')?.textContent).toBe('b');
    expect(c.querySelector('em')?.textContent).toBe('i');
    expect(c.querySelector('u')?.textContent).toBe('u');
    expect(c.querySelector('del')?.textContent).toBe('s');
    expect(c.querySelector('code.text-entity-code')?.textContent).toBe('c');
  });

  it('renders code blocks with their language', () => {
    const c = html('fmt.Println()', [e('pre', 0, 13, { language: 'go' })]);
    const pre = c.querySelector('pre.text-entity-pre')!;
    expect(pre.getAttribute('data-language')).toBe('go');
    expect(pre.querySelector('.code-language')?.textContent).toBe('go');
    expect(pre.querySelector('code')?.textContent).toBe('fmt.Println()');
  });

  it('renders safe links and neutralises unsafe ones', () => {
    const c = html('x.dev site bad', [e('url', 0, 5), e('text_link', 6, 4, { url: 'https://y.dev' }), e('text_link', 11, 3, { url: 'javascript:alert(1)' })]);
    const links = c.querySelectorAll('a');
    expect(links).toHaveLength(2);
    expect(links[0].getAttribute('href')).toBe('https://x.dev/');
    expect(links[1].getAttribute('href')).toBe('https://y.dev/');
    expect(links[1].getAttribute('rel')).toBe('noopener noreferrer');
    expect(c.querySelector('span.text-entity-link')?.textContent).toBe('bad');
  });

  it('links mentions, emails and phones; styles hashtags without a link', () => {
    const c = html('@alice a@b.co +1 555 #tag $USD /start', [
      e('mention', 0, 6),
      e('email', 7, 6),
      e('phone_number', 14, 6),
      e('hashtag', 21, 4),
      e('cashtag', 26, 4),
      e('bot_command', 31, 6),
    ]);
    const hrefs = [...c.querySelectorAll('a')].map((a) => a.getAttribute('href'));
    expect(hrefs).toEqual(['https://t.me/alice', 'mailto:a@b.co', 'tel:+1555']);
    expect([...c.querySelectorAll('span.text-entity-link')].map((s) => s.textContent)).toEqual(['#tag', '$USD', '/start']);
  });

  it('hides spoilers until clicked', () => {
    const c = html('secret', [e('spoiler', 0, 6)]);
    const sp = screen.getByRole('button', { name: '显示剧透内容' });
    expect(sp.classList.contains('revealed')).toBe(false);
    fireEvent.click(sp);
    expect(c.querySelector('.text-entity-spoiler')?.classList.contains('revealed')).toBe(true);
  });

  it('renders quotes, expandable quotes and custom emoji fallbacks', () => {
    const c = html('quote long 😀', [e('blockquote', 0, 5), e('expandable_blockquote', 6, 4), e('custom_emoji', 11, 2, { custom_emoji_id: '99' })]);
    expect(c.querySelector('blockquote.text-entity-quote:not(.expandable)')?.textContent).toBe('quote');
    fireEvent.click(screen.getByRole('button', { name: '展开引用' }));
    expect(c.querySelector('blockquote.expandable')?.classList.contains('expanded')).toBe(true);
    const emoji = c.querySelector('.custom-emoji')!;
    expect(emoji.textContent).toBe('😀');
    expect(emoji.getAttribute('data-custom-emoji-id')).toBe('99');
  });

  it('keeps newlines in the text for pre-wrap rendering', () => {
    expect(html('a\nb', []).textContent).toBe('a\nb');
  });
});
