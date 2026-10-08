import { describe, expect, it } from 'vitest';
import { scrollWithin } from './scroll';

function box(top: number, height: number, extra: Partial<HTMLElement> = {}) {
  return { getBoundingClientRect: () => ({ top, height }), offsetHeight: height, clientHeight: height, ...extra } as unknown as HTMLElement;
}

describe('scrollWithin', () => {
  it('puts an element at the top of its list, below its scroll margin', () => {
    const list = box(50, 800, { scrollTop: 1000 } as Partial<HTMLElement>);
    const el = box(450, 30);
    const getStyle = () => ({ scrollMarginTop: '100px' }) as CSSStyleDeclaration;
    scrollWithin(list, el, 'start', getStyle);
    expect(list.scrollTop).toBe(1000 + 400 - 100);
  });

  it('centres an element in its list', () => {
    const list = box(0, 800, { scrollTop: 0 } as Partial<HTMLElement>);
    scrollWithin(list, box(1000, 200), 'center');
    expect(list.scrollTop).toBe(1000 - 300);
  });
});
