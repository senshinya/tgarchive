import { useEffect, useRef, useState } from 'preact/hooks';

/** Animated .tgs sticker: gzip-compressed Lottie JSON, decompressed natively and played with lottie-web (lazy chunk). */
export function TgsSticker({ src, fallback }: { src: string; fallback: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let cancelled = false;
    let destroy: (() => void) | undefined;
    (async () => {
      const res = await fetch(src);
      if (!res.ok || !res.body) throw new Error(`HTTP ${res.status}`);
      const json = await new Response(res.body.pipeThrough(new DecompressionStream('gzip'))).json();
      const { default: lottie } = await import('lottie-web/build/player/lottie_light');
      if (cancelled || !ref.current) return;
      const anim = lottie.loadAnimation({ container: ref.current, renderer: 'svg', loop: true, autoplay: true, animationData: json });
      destroy = () => anim.destroy();
    })().catch(() => {
      if (!cancelled) setFailed(true);
    });
    return () => {
      cancelled = true;
      destroy?.();
    };
  }, [src]);

  if (failed) return <span class="Sticker-fallback">{fallback || '贴纸'}</span>;
  return <div class="Sticker-lottie" ref={ref} />;
}
