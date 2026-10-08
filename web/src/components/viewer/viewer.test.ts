import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { sourceType } from '../media/util';
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

  it('makes the buffering spinner svg a block, so its box is square and it spins about its own centre', () => {
    expect(bodyOf('.vds-buffering-spinner svg {')).toMatch(/display:\s*block;/);
  });

  it('pins the header to the viewport edges so a long nowrap title cannot widen it past the viewport', () => {
    expect(bodyOf('.MediaViewer-head {')).toMatch(/position:\s*absolute;[\s\S]*left:\s*0;\s*right:\s*0;/);
  });

  it('lets the sender/title block take the remaining space and shrink, while actions never shrink', () => {
    expect(bodyOf('.MediaViewer-sender {')).toMatch(/flex:\s*1 1 auto;[\s\S]*min-width:\s*0;/);
    expect(bodyOf('.MediaViewer-actions {')).toMatch(/flex-shrink:\s*0;/);
  });
});

describe('sourceType', () => {
  it('passes the player a type the browser recognises: QuickTime files play as MP4', () => {
    expect(sourceType('video/quicktime')).toBe('video/mp4');
    expect(sourceType('video/mp4')).toBe('video/mp4');
    expect(sourceType('video/webm')).toBe('video/webm');
    expect(sourceType('')).toBe('video/mp4');
    expect(sourceType('application/octet-stream')).toBe('video/mp4');
  });

  it('keeps the caption and strip at the bottom for videos too, lifting the player controls above them', () => {
    expect(bodyOf('.ViewerOverlay-foot {')).not.toContain('bottom: 4.5rem');
    // Phones only: on desktop the slide itself stays clear of the caption.
    expect(css).toMatch(/@media \(max-width:\s*600px\)\s*\{\s*\.vds-controls\s*\{[^}]*padding-bottom:\s*calc\(var\(--viewer-foot/);
  });
});
