import { defineConfig } from 'vitest/config';

// `npm run dev` proxies the API to a local backend started with REQUIRE_FORWARD_AUTH=false.
const backend = 'http://127.0.0.1:8080';

export default defineConfig({
  oxc: { jsx: { runtime: 'automatic', importSource: 'preact' } },
  build: { outDir: 'dist', emptyOutDir: true, assetsDir: 'assets', target: 'es2022' },
  server: { proxy: { '/api': backend, '/media': backend, '/avatars': backend } },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.{ts,tsx}'],
    setupFiles: ['src/test/setup.ts'],
  },
});
