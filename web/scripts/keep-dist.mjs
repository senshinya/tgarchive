// vite empties dist/ on every build; the tracked placeholder keeps `go:embed all:dist` compiling in fresh checkouts.
import { writeFileSync } from 'node:fs';

writeFileSync(new URL('../dist/.gitkeep', import.meta.url), '');
