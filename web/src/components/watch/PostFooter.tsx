import { Sparkles } from 'lucide-preact';
import type { ComponentChildren } from 'preact';
import { useState } from 'preact/hooks';
import type { PostStats, ReactionStat } from '../../api/types';
import { formatFullDate } from '../../lib/format';
import { formatCount } from '../../lib/watchCond';
import { ReactionIcon } from './ReactionIcon';
import './watch.scss';

/** Telegram's order: paid stars first, then as the server listed them (by count). */
export function orderReactions(list: ReactionStat[]): ReactionStat[] {
  return [...list.filter((r) => r.key === 'paid'), ...list.filter((r) => r.key !== 'paid')];
}

/** A watched channel post's reactions as they stood when it was archived, Web A style: pills in
 * a wrapping row (inside the bubble, ending with the post's meta; or under media-only posts),
 * plus the mark that tells why it was archived. */
export function PostReactions({ stats, meta, outside }: { stats: PostStats; meta?: ComponentChildren; outside?: boolean }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <div class={`PostReactions${outside ? ' outside' : ''}`}>
        {orderReactions(stats.reactions).map((r) => (
          <span class={`PostReaction${r.key === 'paid' ? ' paid' : ''}`} key={r.key}>
            <ReactionIcon stat={r} />
            <span class="PostReaction-count">{formatCount(r.count)}</span>
          </span>
        ))}
        {stats.hit && (
          <button
            type="button"
            class={`PostReaction PostHit${open ? ' open' : ''}`}
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
        {meta}
      </div>
      {open && stats.hit && (
        <div class={`PostHit-detail${outside ? ' outside' : ''}`}>
          <div class="PostHit-title">命中于 {formatFullDate(stats.hit.at)}</div>
          {stats.hit.reasons.map((r) => (
            <div key={r}>{r}</div>
          ))}
          <div class="PostHit-counts">
            浏览 {formatCount(stats.views)} · 转发 {formatCount(stats.forwards)} · 评论 {formatCount(stats.replies)}
          </div>
        </div>
      )}
    </>
  );
}
