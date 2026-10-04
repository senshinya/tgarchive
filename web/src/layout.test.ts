import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
// Ambient types for the two Node builtins above: see test/node-fs.d.ts.

// Vitest mocks `?raw` CSS imports to an empty string, so this reads the file directly instead.
const css = readFileSync(join(process.cwd(), 'src/layout.scss'), 'utf-8');

/** Pulls out the `background: ...;` declaration of the first rule whose selector contains `selector`. */
function backgroundOf(selector: string): string {
  const ruleStart = css.indexOf(selector);
  expect(ruleStart).toBeGreaterThanOrEqual(0);
  const braceStart = css.indexOf('{', ruleStart);
  const braceEnd = css.indexOf('}', braceStart);
  const body = css.slice(braceStart, braceEnd);
  const match = body.match(/background:\s*([^;]+);/);
  expect(match).toBeTruthy();
  return match![1].trim();
}

describe('layout.scss', () => {
  it('gives #Main a background distinct from the settings canvas / right-column card color', () => {
    // --color-background-secondary is also .settings-content's and #RightColumn's background
    // (see settings.scss, webA-design.md §8). #Main sharing that exact token made the gap
    // around the left island visually fuse with the settings canvas below the last card, so the
    // island looked like it stopped early instead of the gray continuing to the bottom.
    const mainBg = backgroundOf('#Main {');
    expect(mainBg).not.toBe('var(--color-background-secondary)');
    expect(mainBg).toBe('var(--color-background-secondary-accent)');
  });
});
