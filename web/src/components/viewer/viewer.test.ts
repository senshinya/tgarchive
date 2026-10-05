import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
// Ambient types for the two Node builtins above: see test/node-fs.d.ts.

// Vitest mocks `?raw` CSS imports to an empty string, so this reads the file directly instead
// (same approach as ../../layout.test.ts).
const css = readFileSync(join(process.cwd(), 'src/components/viewer/viewer.scss'), 'utf-8');

/** The declaration body (balanced `{`...`}`, including any nested `@media` blocks) of the first
 * rule whose selector contains `selector`. */
function bodyOf(selector: string): string {
  const ruleStart = css.indexOf(selector);
  expect(ruleStart).toBeGreaterThanOrEqual(0);
  const braceStart = css.indexOf('{', ruleStart);
  let depth = 0;
  let i = braceStart;
  for (; i < css.length; i++) {
    if (css[i] === '{') depth++;
    else if (css[i] === '}' && --depth === 0) break;
  }
  return css.slice(braceStart, i + 1);
}

describe('viewer.scss', () => {
  it('hides the zoom in/out buttons at <=600px, so the 4-icon header no longer wraps the sender/date to 3 lines', () => {
    const body = bodyOf('.MediaViewer-actions {');
    expect(body).toMatch(/@media \(max-width:\s*600px\)\s*\{\s*\.zoom-btn\s*\{\s*display:\s*none;/);
  });

  it('keeps the date line single-line with ellipsis at all widths (not just <=600px)', () => {
    const body = bodyOf('.MediaViewer-date {');
    expect(body).toMatch(/overflow:\s*hidden;\s*text-overflow:\s*ellipsis;\s*white-space:\s*nowrap;/);
  });

  it('lets the sender/title block take the remaining space and shrink, while actions never shrink', () => {
    expect(bodyOf('.MediaViewer-sender {')).toMatch(/flex:\s*1 1 auto;[\s\S]*min-width:\s*0;/);
    expect(bodyOf('.MediaViewer-actions {')).toMatch(/flex-shrink:\s*0;/);
  });
});
