// Telegram Web A's default chat wallpaper, at rest: a four-colour distance-weighted gradient
// (port of drawStaticGradient in Ajaxy/telegram-tt src/util/gradientBackground.ts, GPL-3.0)
// under the official doodle pattern. Web A animates the gradient only when the user sends a
// message; this app never sends, so the resting frame is all that's ever visible.

export const LIGHT_COLORS = ['#bdcd8c', '#8eba89', '#83b28f', '#c5d3b0'];
export const DARK_COLORS = ['#4f5bd5', '#962fbf', '#dd6cb9', '#fec496'];
export const GRADIENT_SIZE = 64;

// The renderer's eight key points; at rest the four colours sit on every other one.
const KEY_POINTS = [
  [0.265, 0.582],
  [0.176, 0.918],
  [1 - 0.585, 1 - 0.164],
  [0.644, 0.755],
  [1 - 0.265, 1 - 0.582],
  [1 - 0.176, 1 - 0.918],
  [0.585, 0.164],
  [1 - 0.644, 1 - 0.755],
];
const POSITIONS = [KEY_POINTS[0], KEY_POINTS[2], KEY_POINTS[4], KEY_POINTS[6]];
const BLEND_POWER = 3;

function rgb(hex: string): [number, number, number] {
  const v = hex.replace('#', '');
  return [parseInt(v.slice(0, 2), 16), parseInt(v.slice(2, 4), 16), parseInt(v.slice(4, 6), 16)];
}

/** RGBA pixels of the resting gradient, row-major, size×size. */
export function gradientPixels(colors: string[], size = GRADIENT_SIZE): Uint8ClampedArray {
  const vecs = POSITIONS.map((_, i) => rgb(colors[i % colors.length]));
  const data = new Uint8ClampedArray(size * size * 4);
  for (let row = 0; row < size; row++) {
    for (let col = 0; col < size; col++) {
      const x = col / size;
      const y = row / size;
      const dist = POSITIONS.map(([cx, cy]) => Math.hypot(x - cx, y - cy));
      const min = Math.min(...dist);
      const w = dist.map((d) => (1 - (d - min)) ** BLEND_POWER);
      const total = Math.abs(w.reduce((s, v) => s + v, 0));
      let r = 0;
      let g = 0;
      let b = 0;
      for (let i = 0; i < vecs.length; i++) {
        const k = w[i] / total;
        r += vecs[i][0] * k;
        g += vecs[i][1] * k;
        b += vecs[i][2] * k;
      }
      const o = (row * size + col) * 4;
      data[o] = r;
      data[o + 1] = g;
      data[o + 2] = b;
      data[o + 3] = 255;
    }
  }
  return data;
}

/** Paints the gradient onto the canvas; false when no 2D context is available. */
export function paintGradient(canvas: HTMLCanvasElement, colors: string[]): boolean {
  canvas.width = GRADIENT_SIZE;
  canvas.height = GRADIENT_SIZE;
  const ctx = canvas.getContext('2d');
  if (!ctx) return false;
  const image = ctx.createImageData(GRADIENT_SIZE, GRADIENT_SIZE);
  image.data.set(gradientPixels(colors));
  ctx.putImageData(image, 0, 0);
  return true;
}
