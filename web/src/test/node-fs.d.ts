// Minimal ambient types for the couple of Node builtins a source-text regression test needs
// (layout.test.ts). `@types/node` isn't a project dependency (constraints.md: no new deps) —
// Vitest's own runtime provides these globals even without it, so just type the bit we use.
declare const process: { cwd(): string };

declare module 'node:fs' {
  export function readFileSync(path: string, encoding: string): string;
}

declare module 'node:path' {
  export function join(...parts: string[]): string;
}
