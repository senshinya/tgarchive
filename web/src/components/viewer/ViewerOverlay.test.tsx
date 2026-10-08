import { fireEvent, render, screen } from '@testing-library/preact';
import { describe, expect, it, vi } from 'vitest';
import type { ViewerItem } from '../../state/store';
import { ViewerOverlay } from './ViewerOverlay';

const item = (mediaId: number, kind = 'photo', extra: Partial<ViewerItem> = {}): ViewerItem => ({
  mediaId,
  kind,
  date: 1_790_000_000,
  text: '',
  entities: [],
  ...extra,
});

function setup(items: ViewerItem[], index: number) {
  const onPick = vi.fn();
  const onClose = vi.fn();
  const onZoom = vi.fn();
  const r = render(<ViewerOverlay items={items} index={index} title="Alice" onPick={onPick} onClose={onClose} onZoom={onZoom} />);
  return { ...r, onPick, onClose, onZoom };
}

describe('ViewerOverlay', () => {
  it('shows title, position, and a strip that highlights the current item and jumps on click', () => {
    const items = [item(1), item(2, 'video', { thumbId: 22 }), item(3, 'video')];
    const { onPick, container } = setup(items, 1);
    expect(screen.getByText('Alice')).toBeTruthy();
    expect(screen.getByText(/2 \/ 3/)).toBeTruthy();
    const tabs = screen.getAllByRole('tab');
    expect(tabs).toHaveLength(3);
    expect(tabs[1].getAttribute('aria-selected')).toBe('true');
    // photo uses itself, a video its thumbnail, a video without one an icon
    expect(tabs[0].querySelector('img')!.getAttribute('src')).toBe('/media/1');
    expect(tabs[1].querySelector('img')!.getAttribute('src')).toBe('/media/22');
    expect(tabs[2].querySelector('img')).toBeNull();
    fireEvent.click(tabs[2]);
    expect(onPick).toHaveBeenCalledWith(2);
    expect(container.querySelector('.ViewerOverlay')!.getAttribute('data-kind')).toBe('video');
  });

  it('hides the strip for a single item and offers zoom on photos only', () => {
    const { onZoom, onClose, rerender } = setup([item(1)], 0);
    expect(screen.queryByRole('tablist')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: '放大' }));
    expect(onZoom).toHaveBeenCalledWith(1);
    fireEvent.click(screen.getByRole('button', { name: '关闭' }));
    expect(onClose).toHaveBeenCalled();
    rerender(<ViewerOverlay items={[item(1, 'video')]} index={0} title="A" onPick={vi.fn()} onClose={vi.fn()} onZoom={onZoom} />);
    expect(screen.queryByRole('button', { name: '放大' })).toBeNull();
    expect(screen.getByRole('link', { name: '下载' }).getAttribute('href')).toBe('/media/1?download=1');
  });

  it('tells the video controls how tall the caption and strip are, so they sit above them', () => {
    const observers: (() => void)[] = [];
    const saved = globalThis.ResizeObserver;
    globalThis.ResizeObserver = class {
      constructor(cb: () => void) {
        observers.push(cb);
      }
      observe() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver;
    const root = document.createElement('div');
    root.className = 'pswp';
    document.body.appendChild(root);
    render(
      <ViewerOverlay items={[item(1, 'video', { text: 'caption' }), item(2)]} index={0} title="A" onPick={vi.fn()} onClose={vi.fn()} />,
      {
        container: root,
      },
    );
    const foot = root.querySelector('.ViewerOverlay-foot') as HTMLElement;
    Object.defineProperty(foot, 'offsetHeight', { configurable: true, value: 120 });
    observers.forEach((cb) => cb());
    expect(root.style.getPropertyValue('--viewer-foot')).toBe('120px');
    globalThis.ResizeObserver = saved;
    root.remove();
  });
});
