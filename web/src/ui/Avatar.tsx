import { useState } from 'preact/hooks';
import { initials, peerColorIndex } from '../lib/format';
import './ui.scss';

export const AVATAR_SIZES = { mini: 24, tiny: 32, small: 34, medium: 44, large: 54, jumbo: 120 } as const;
export type AvatarSize = keyof typeof AVATAR_SIZES;

interface Props {
  name: string;
  peerId: number;
  src?: string | null;
  size: AvatarSize;
}

export function Avatar({ name, peerId, src, size }: Props) {
  const [failed, setFailed] = useState(false);
  const px = AVATAR_SIZES[size];
  const showImage = Boolean(src) && !failed;
  return (
    <div
      class={`Avatar size-${size}`}
      style={{
        width: `${px}px`,
        height: `${px}px`,
        '--avatar-color': `var(--peer-${peerColorIndex(peerId)})`,
        fontSize: `${Math.max(px / 2 - 4, 8)}px`,
      }}
      aria-hidden="true"
    >
      {showImage ? (
        <img src={src!} alt="" loading="lazy" decoding="async" onError={() => setFailed(true)} />
      ) : (
        <span class="Avatar-initials">{initials(name)}</span>
      )}
    </div>
  );
}
