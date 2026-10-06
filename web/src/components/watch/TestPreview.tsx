import { Check, X } from 'lucide-preact';
import type { TestPost } from '../../api/types';
import { formatListTime, previewText } from '../../lib/format';
import { formatCount } from '../../lib/watchCond';
import { Spinner } from '../../ui/Spinner';
import { ReactionIcon } from './ReactionIcon';
import './watch.scss';

/** The channel's latest posts judged against the condition being edited. */
export function TestPreview({ posts, loading, error, judged }: { posts: TestPost[] | null; loading: boolean; error: string; judged: boolean }) {
  if (error) return <p class="settings-description">{error}</p>;
  if (!posts) {
    return (
      <div class="settings-loading">
        <Spinner size={32} />
      </div>
    );
  }
  if (posts.length === 0) return <p class="settings-description">频道里还没有帖子</p>;
  return (
    <div class={`TestPreview${loading ? ' loading' : ''}`}>
      {posts.map((p) => (
        <div key={p.tg_message_id} class={`TestPost${judged ? (p.hit ? ' hit' : ' miss') : ''}`}>
          <span class="TestPost-mark" aria-label={judged ? (p.hit ? '满足' : '不满足') : undefined}>
            {judged && (p.hit ? <Check size={18} /> : <X size={18} />)}
          </span>
          <span class="TestPost-body">
            <span class="TestPost-row">
              <span class="TestPost-text">{previewText(p.kind, p.text)}</span>
              <span class="TestPost-time">{formatListTime(p.date)}</span>
            </span>
            <span class="TestPost-stats">
              {p.stats.reactions.slice(0, 6).map((r) => (
                <span key={r.key} class="TestPost-reaction">
                  <ReactionIcon stat={r} />
                  {formatCount(r.count)}
                </span>
              ))}
              <span class="TestPost-views">浏览 {formatCount(p.stats.views)}</span>
            </span>
            {p.hit && p.reasons.length > 0 && <span class="TestPost-reasons">{p.reasons.join('，')}</span>}
          </span>
        </div>
      ))}
    </div>
  );
}
