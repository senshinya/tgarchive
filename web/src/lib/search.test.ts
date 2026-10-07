import { describe, expect, it } from 'vitest';
import { highlightParts } from './search';

describe('highlightParts', () => {
  it('splits a snippet into plain and marked runs', () => {
    expect(highlightParts('今天天气很好', [[2, 2]])).toEqual([
      { text: '今天', mark: false },
      { text: '天气', mark: true },
      { text: '很好', mark: false },
    ]);
  });

  it('handles adjacent, leading and trailing matches', () => {
    expect(highlightParts('abcd', [[0, 1], [1, 1], [3, 1]])).toEqual([
      { text: 'a', mark: true },
      { text: 'b', mark: true },
      { text: 'c', mark: false },
      { text: 'd', mark: true },
    ]);
  });

  it('counts in UTF-16 units like the server, and ignores ranges out of bounds or overlapping', () => {
    expect(highlightParts('😀天气', [[2, 2], [3, 5], [9, 1]])).toEqual([
      { text: '😀', mark: false },
      { text: '天气', mark: true },
    ]);
    expect(highlightParts('', [])).toEqual([]);
  });
});
