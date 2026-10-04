import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
// Ambient types for the two Node builtins above: see test/node-fs.d.ts.

const html = readFileSync(join(process.cwd(), 'index.html'), 'utf-8');

describe('index.html', () => {
  it('does not set viewport-fit=cover (no env(safe-area-inset-*) padding uses it; let the browser keep content out of notches on its own)', () => {
    expect(html).not.toMatch(/viewport-fit\s*=\s*cover/);
  });

  it('keeps the rest of the viewport meta intact', () => {
    expect(html).toMatch(/<meta name="viewport" content="width=device-width, initial-scale=1"\s*\/?>/);
  });
});
