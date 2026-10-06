import { Sparkles } from 'lucide-preact';
import { useState } from 'preact/hooks';
import type { PostStats } from '../../api/types';
import { formatFullDate } from '../../lib/format';
import { formatCount } from '../../lib/watchCond';
import { ReactionIcon } from './ReactionIcon';
import './watch.scss';

/** Reactions under a watched channel post, as they stood when it was archived, plus the mark
 * that opens why it was archived. */
export function PostFooter({ stats }: { stats: PostStats }) {
  const [open, setOpen] = useState(false);
  return (
    <div class="PostFooter">
      <div class="PostReactions">
        {stats.reactions.map((r) => (
          <span class="PostReaction" key={r.key}>
            <ReactionIcon stat={r} />
            <span class="PostReaction-count">{formatCount(r.count)}</span>
          </span>
        ))}
        {stats.hit && (
          <button
            type="button"
            class={`PostHit${open ? ' open' : ''}`}
            aria-expanded={open}
            title="为何存档"
            onClick={(e) => {
              e.stopPropagation();
              setOpen(!open);
            }}
          >
            <Sparkles size={14} />
          </button>
        )}
      </div>
      {open && stats.hit && (
        <div class="PostHit-detail">
          <div class="PostHit-title">命中于 {formatFullDate(stats.hit.at)}</div>
          {stats.hit.reasons.map((r) => (
            <div key={r}>{r}</div>
          ))}
          <div class="PostHit-counts">
            浏览 {formatCount(stats.views)} · 转发 {formatCount(stats.forwards)} · 评论 {formatCount(stats.replies)}
          </div>
        </div>
      )}
    </div>
  );
}
