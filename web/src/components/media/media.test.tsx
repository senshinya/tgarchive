import { act, fireEvent, screen } from '@testing-library/preact';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Message } from '../../api/types';
import { fakeApi, makeMedia, makeMessage } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { Album } from './Album';
import { Animation } from './Animation';
import { Contact, Dice, Location, Poll } from './Cards';
import { Document } from './Document';
import { MessageMedia } from './MessageMedia';
import { Photo } from './Photo';
import { Sticker } from './Sticker';
import { Video } from './Video';
import { IMAGE_DOCUMENT_MAX_BYTES, displayKind, playUrl } from './util';
import { Voice } from './Voice';
import { VideoNote } from './VideoNote';
import { setSilent } from '../../lib/silent';
import { toViewerItems } from '../viewer/MediaViewer';

afterEach(() => vi.unstubAllGlobals());

const photoMsg = (over: Partial<Message> = {}, state: 'done' | 'pending' | 'failed' | 'too_large' = 'done') =>
  makeMessage({ kind: 'photo', text: '', media: [makeMedia({ id: 100, state, error: state === 'failed' ? 'timeout' : '' })], ...over });

describe('Photo', () => {
  it('shows the archived image and opens the viewer on click', () => {
    const onOpen = vi.fn();
    const { container } = renderWithStore(<Photo msg={photoMsg()} onOpen={onOpen} />);
    const img = container.querySelector('img')!;
    expect(img.getAttribute('src')).toBe('/media/100');
    expect((container.firstChild as HTMLElement).style.width).toBe('464px');
    fireEvent.click(img);
    expect(onOpen).toHaveBeenCalled();
  });

  it('shows pending and too_large placeholders', () => {
    renderWithStore(<Photo msg={photoMsg({}, 'pending')} onOpen={() => {}} />);
    expect(screen.getByText('排队中')).toBeTruthy();
    renderWithStore(<Photo msg={photoMsg({}, 'too_large')} onOpen={() => {}} />);
    expect(screen.getByText('文件超过存档上限')).toBeTruthy();
  });

  it('shows a progress ring and bytes while downloading', () => {
    const r = renderWithStore(<Photo msg={photoMsg({}, 'pending')} onOpen={() => {}} />);
    act(() => {
      r.store.progress.value = new Map([[100, { done: 512 * 1024, total: 2 * 1024 * 1024 }]]);
    });
    expect(screen.getByText('512 KB / 2 MB')).toBeTruthy();
    expect(screen.getByRole('progressbar').getAttribute('aria-valuenow')).toBe('25');
    act(() => {
      r.store.progress.value = new Map([[100, { done: 0, total: 2 * 1024 * 1024 }]]);
    });
    // Nothing received yet (e.g. a bot download whose progress is unknown): spin, show the size.
    expect(screen.getByText('下载中… · 2 MB')).toBeTruthy();
    expect(screen.getByRole('progressbar').getAttribute('aria-valuenow')).toBeNull();
  });

  it('retries failed media through the store', async () => {
    const api = fakeApi();
    renderWithStore(<Photo msg={photoMsg({}, 'failed')} onOpen={() => {}} />, api);
    expect(screen.getByText('timeout')).toBeTruthy();
    await act(async () => {
      fireEvent.click(screen.getByText('重试'));
    });
    expect(api.retryMedia).toHaveBeenCalledWith(100);
  });

  it('blurs spoiler media until revealed', () => {
    const onOpen = vi.fn();
    const { container } = renderWithStore(<Photo msg={photoMsg({ extra: { spoiler: true } })} onOpen={onOpen} />);
    expect(container.querySelector('img')!.className).toBe('media-spoiler-blur');
    fireEvent.click(screen.getByText('显示剧透内容'));
    expect(container.querySelector('img')!.className).toBe('');
    expect(onOpen).not.toHaveBeenCalled();
  });
});

describe('Video', () => {
  it('shows the thumbnail, duration and opens on play', () => {
    const onOpen = vi.fn();
    const msg = makeMessage({
      kind: 'video',
      media: [makeMedia({ id: 5, kind: 'video', duration: 75 }), makeMedia({ id: 6, role: 'thumb' })],
    });
    const { container } = renderWithStore(<Video msg={msg} onOpen={onOpen} />);
    expect(container.querySelector('img')!.getAttribute('src')).toBe('/media/6');
    expect(screen.getByText('1:15')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: '播放视频' }));
    expect(onOpen).toHaveBeenCalled();
  });

  it('shows the pending status when the thumbnail is ready but the main file is not', () => {
    const msg = makeMessage({
      kind: 'video',
      media: [makeMedia({ id: 5, kind: 'video', duration: 75, state: 'pending' }), makeMedia({ id: 6, role: 'thumb' })],
    });
    renderWithStore(<Video msg={msg} onOpen={() => {}} />);
    expect(screen.getByText('排队中')).toBeTruthy();
  });

  it('shows a retry button for failed media that calls the store', async () => {
    const api = fakeApi();
    const msg = makeMessage({
      kind: 'video',
      media: [
        makeMedia({ id: 5, kind: 'video', duration: 75, state: 'failed', error: 'timeout' }),
        makeMedia({ id: 6, role: 'thumb' }),
      ],
    });
    renderWithStore(<Video msg={msg} onOpen={() => {}} />, api);
    await act(async () => {
      fireEvent.click(screen.getByText('重试'));
    });
    expect(api.retryMedia).toHaveBeenCalledWith(5);
  });
});

describe('Document', () => {
  const doc = (state: 'done' | 'failed' | 'too_large') =>
    makeMessage({
      kind: 'document',
      media: [makeMedia({ id: 9, kind: 'document', mime: 'application/pdf', file_name: 'report.pdf', size: 1536, state, error: state === 'failed' ? 'bad' : '' })],
    });

  it('links archived files for download with name and size', () => {
    const { container } = renderWithStore(<Document msg={doc('done')} />);
    const a = container.querySelector('a.File-icon')!;
    expect(a.getAttribute('href')).toBe('/media/9?download=1');
    expect(a.getAttribute('download')).toBe('report.pdf');
    expect((a as HTMLElement).style.getPropertyValue('--file-color')).toBe('var(--color-error)');
    expect(screen.getByText('report.pdf')).toBeTruthy();
    expect(screen.getByText('1.5 KB')).toBeTruthy();
    expect(screen.getByText('pdf')).toBeTruthy();
  });

  it('shows download progress in place of the size', () => {
    const pending = makeMessage({
      kind: 'document',
      media: [makeMedia({ id: 9, kind: 'document', mime: 'application/octet-stream', file_name: 'big.zip', size: 4096, state: 'pending' })],
    });
    const r = renderWithStore(<Document msg={pending} />);
    expect(screen.getByText('排队中')).toBeTruthy();
    expect(screen.getByText('· 4 KB', { exact: false })).toBeTruthy();
    act(() => {
      r.store.progress.value = new Map([[9, { done: 1024, total: 4096 }]]);
    });
    expect(screen.getByText('1 KB / 4 KB')).toBeTruthy();
    expect(screen.queryByText('· 4 KB', { exact: false })).toBeNull();
  });

  it('shows failure with retry and the too_large note', () => {
    const { container } = renderWithStore(<Document msg={doc('failed')} />);
    expect(container.querySelector('a')).toBeNull();
    expect(screen.getByText('下载失败：bad')).toBeTruthy();
    expect(screen.getByText('重试')).toBeTruthy();
    renderWithStore(<Document msg={doc('too_large')} />);
    expect(screen.getByText('文件超过存档上限')).toBeTruthy();
  });
});

describe('Voice', () => {
  it('draws one bar per spike and toggles playback', () => {
    const msg = makeMessage({ kind: 'voice', media: [makeMedia({ kind: 'voice', mime: 'audio/ogg', duration: 15, waveform: btoa('ÿ\u0001\u0010') })] });
    const { container } = renderWithStore(<Voice msg={msg} />);
    expect(container.querySelectorAll('rect')).toHaveLength(30);
    expect(screen.getByText('0:15')).toBeTruthy();
    const play = vi.spyOn(HTMLMediaElement.prototype, 'play');
    fireEvent.click(screen.getByRole('button', { name: '播放' }));
    expect(play).toHaveBeenCalled();
    fireEvent.play(container.querySelector('audio')!);
    expect(screen.getByRole('button', { name: '暂停' })).toBeTruthy();
  });
});

describe('Sticker', () => {
  const sticker = (mime: string) =>
    makeMessage({ kind: 'sticker', extra: { emoji: '😺' }, media: [makeMedia({ kind: 'sticker', mime, width: 512, height: 256 })] });

  it('renders webp as an image and webm as a looping video', () => {
    const { container } = renderWithStore(<Sticker msg={sticker('image/webp')} />);
    expect(container.querySelector('img')!.getAttribute('alt')).toBe('😺');
    expect((container.firstChild as HTMLElement).style.getPropertyValue('--sticker-h')).toBe('0.5');
    const v = renderWithStore(<Sticker msg={sticker('video/webm')} />).container.querySelector('video')!;
    expect(v.loop).toBe(true);
    expect(v.muted).toBe(true);
  });

  it('falls back to the emoji when a tgs sticker cannot load', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => Promise.reject(new TypeError('offline'))));
    renderWithStore(<Sticker msg={sticker('application/x-tgsticker')} />);
    expect(await screen.findByText('😺')).toBeTruthy();
  });
});

describe('cards', () => {
  it('renders venue with OpenStreetMap link', () => {
    const msg = makeMessage({ kind: 'venue', extra: { latitude: 31.2, longitude: 121.5, title: '外滩', address: '中山东一路' } });
    const { container } = renderWithStore(<Location msg={msg} />);
    expect(container.querySelector('a')!.getAttribute('href')).toBe('https://www.openstreetmap.org/?mlat=31.2&mlon=121.5#map=16/31.2/121.5');
    expect(screen.getByText('外滩')).toBeTruthy();
    expect(screen.getByText('中山东一路')).toBeTruthy();
  });

  it('renders a bare location with coordinates', () => {
    renderWithStore(<Location msg={makeMessage({ kind: 'location', extra: { latitude: 1.5, longitude: 2 } })} />);
    expect(screen.getByText('1.500000, 2.000000')).toBeTruthy();
  });

  it('renders contact, poll and dice', () => {
    renderWithStore(<Contact msg={makeMessage({ kind: 'contact', extra: { first_name: 'Bob', phone_number: '+100' } })} />);
    expect(screen.getByText('Bob')).toBeTruthy();
    expect(screen.getByText('+100')).toBeTruthy();
    renderWithStore(
      <Poll
        msg={makeMessage({
          kind: 'poll',
          extra: { question: '吃什么', options: [{ text: '面', voter_count: 3 }, { text: '饭', voter_count: 1 }], total_voter_count: 4, is_anonymous: true, poll_type: 'regular', multiple: false },
        })}
      />,
    );
    expect(screen.getByText('75%')).toBeTruthy();
    expect(screen.getByText('25%')).toBeTruthy();
    expect(screen.getByText('4 人投票')).toBeTruthy();
    expect(screen.getByText('匿名投票')).toBeTruthy();
    renderWithStore(<Dice msg={makeMessage({ kind: 'dice', extra: { emoji: '🎯', value: 6 } })} />);
    expect(screen.getByText('🎯')).toBeTruthy();
    expect(screen.getByText('点数：6')).toBeTruthy();
  });
});

describe('Album and MessageMedia', () => {
  it('lays out photo albums as positioned tiles', () => {
    const msgs = [1, 2, 3].map((id) => photoMsg({ id, media: [makeMedia({ id: 100 + id, width: 900, height: 1600 })] }));
    const onOpen = vi.fn();
    const { container } = renderWithStore(<Album msgs={msgs} onOpen={onOpen} />);
    const tiles = container.querySelectorAll<HTMLElement>('.Album-tile');
    expect(tiles).toHaveLength(3);
    expect(tiles[0].style.left).toBe('0%');
    expect(tiles[1].getAttribute('data-message-id')).toBe('2');
    fireEvent.click(tiles[2].querySelector('img')!);
    expect(onOpen).toHaveBeenCalledWith(msgs[2]);
  });

  it('stacks document albums as rows', () => {
    const msgs = [1, 2].map((id) => makeMessage({ id, kind: 'document', media: [makeMedia({ id: id + 10, kind: 'document', mime: 'application/octet-stream', file_name: `f${id}.txt` })] }));
    const { container } = renderWithStore(<Album msgs={msgs} onOpen={() => {}} />);
    expect(container.querySelectorAll('.Album-list-item')).toHaveLength(2);
    expect(screen.getByText('f2.txt')).toBeTruthy();
  });

  it('renders nothing for text and unsupported kinds', () => {
    const { container } = renderWithStore(<MessageMedia msg={makeMessage({ kind: 'other' })} onOpen={() => {}} />);
    expect(container.innerHTML).toBe('');
  });
});

describe('images sent as files', () => {
  const imageDoc = (mime: string, size = 870_743) =>
    makeMessage({
      id: 7,
      kind: 'document',
      text: '',
      media: [makeMedia({ id: 70, kind: 'document', mime, size, file_name: 'GIF_1.gif', width: 536, height: 1000 })],
    });

  it('shows a GIF, PNG, JPEG or WebP file inline and opens the viewer', () => {
    const onOpen = vi.fn();
    const { container } = renderWithStore(<MessageMedia msg={imageDoc('image/gif')} onOpen={onOpen} />);
    const img = container.querySelector('.Photo img')!;
    expect(img.getAttribute('src')).toBe('/media/70');
    fireEvent.click(img);
    expect(onOpen).toHaveBeenCalled();
    for (const mime of ['image/png', 'image/jpeg', 'image/webp']) expect(displayKind(imageDoc(mime))).toBe('photo');
  });

  it('keeps other files, and images over the limit, as file rows', () => {
    expect(displayKind(imageDoc('image/heic'))).toBe('document');
    expect(displayKind(imageDoc('image/svg+xml'))).toBe('document');
    const big = imageDoc('image/gif', IMAGE_DOCUMENT_MAX_BYTES + 1);
    expect(displayKind(big)).toBe('document');
    const { container } = renderWithStore(<MessageMedia msg={big} onOpen={() => {}} />);
    expect(container.querySelector('.File')).toBeTruthy();
  });

  it('pages through the viewer as a photo', () => {
    expect(toViewerItems([imageDoc('image/gif')])).toEqual([expect.objectContaining({ mediaId: 70, kind: 'photo', mime: 'image/gif' })]);
    expect(toViewerItems([imageDoc('application/pdf')])).toEqual([]);
  });
});

describe('server-side copies of unplayable videos', () => {
  afterEach(() => vi.restoreAllMocks());

  it('plays the H.264 copy only when this browser cannot decode the original', () => {
    const can = vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('');
    expect(playUrl(5, 'av1')).toBe('/media/5?compat=1');
    expect(playUrl(5, 'mpeg4')).toBe('/media/5?compat=1');
    expect(playUrl(5)).toBe('/media/5');
    can.mockReturnValue('probably');
    expect(playUrl(5, 'av1')).toBe('/media/5');
    expect(playUrl(5, 'mpeg4')).toBe('/media/5?compat=1'); // no browser plays MPEG-4 Part 2
  });

  it('feeds the copy to inline players and the viewer', () => {
    vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('');
    const msg = makeMessage({
      id: 8,
      kind: 'animation',
      text: '',
      media: [makeMedia({ id: 80, kind: 'animation', mime: 'video/mp4', compat_codec: 'av1' })],
    });
    const { container } = renderWithStore(<Animation msg={msg} onOpen={() => {}} />);
    expect(container.querySelector('video')!.getAttribute('src')).toBe('/media/80?compat=1');
    expect(toViewerItems([msg])).toEqual([expect.objectContaining({ mediaId: 80, compatCodec: 'av1' })]);
  });
});

describe('静音模式', () => {
  afterEach(() => setSilent(false));

  it('mutes voice messages and keeps a tapped round video silent', () => {
    setSilent(true);
    const voice = makeMessage({ kind: 'voice', media: [makeMedia({ kind: 'voice', mime: 'audio/ogg', duration: 3 })] });
    const r = renderWithStore(<Voice msg={voice} />);
    expect(r.container.querySelector('audio')!.muted).toBe(true);
    const note = makeMessage({ kind: 'video_note', media: [makeMedia({ id: 90, kind: 'video_note', mime: 'video/mp4' })] });
    const n = renderWithStore(<VideoNote msg={note} />);
    const v = n.container.querySelector('video')!;
    fireEvent.click(v);
    expect(v.muted).toBe(true);
    act(() => setSilent(false));
    expect(r.container.querySelector('audio')!.muted).toBe(false);
  });
});
