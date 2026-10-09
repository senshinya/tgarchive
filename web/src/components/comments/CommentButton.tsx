import type { Commenter, PostStats } from '../../api/types';
import { avatarUrl } from '../../api/client';
import { navigate } from '../../lib/router';
import { formatCount } from '../../lib/watchCond';
import { Avatar } from '../../ui/Avatar';
import './comments.scss';

// Web A's font icons "comments", "message" and "next" (32×32 artboards).
export function CommentsIcon() {
  return (
    <svg class="comments-icon icon-comments" viewBox="0 0 32 32" aria-hidden="true">
      <path d="M16 26.856c7.364 0 13.333-5.415 13.333-12.095S23.363 2.666 16 2.666c-7.364 0-13.333 5.415-13.333 12.095 0 3.81 1.783 6.932 4.818 9.149.388.284.746 1.859-.12 3.182a7.42 7.42 0 0 1-.505.68l-.246.286-.521.571c-.279.316-.389.51-.131.617.333.14 2.304.209 3.726-.587.53-.297.961-.593 1.316-.868l.33-.266.285-.247.548-.499.167-.14c.156-.12.279-.178.393-.152 1.047.24 2.143.367 3.272.367z" />
    </svg>
  );
}

function MessageIcon() {
  return (
    <svg class="comments-icon icon-message" viewBox="0 0 32 32" aria-hidden="true">
      <path d="M16 1.56c7.975 0 14.44 6.465 14.44 14.44S23.975 30.44 16 30.44c-2.621 0-5.084-.7-7.205-1.923l-.006-.004-.061-.036-.045.018-3.427 1.141c-.337.113-.664.224-.935.287-.254.06-.669.134-1.104-.02-.521-.187-.933-.598-1.12-1.12-.154-.435-.078-.851-.018-1.106.062-.27.173-.598.285-.937l.001-.005 1.14-3.418.016-.046-.034-.059-.004-.008C2.26 21.083 1.56 18.621 1.56 16 1.56 8.025 8.025 1.56 16 1.56m0 2.213C9.248 3.773 3.773 9.248 3.773 16c0 2.223.594 4.305 1.628 6.099l.004.008c.075.129.224.37.295.63.06.215.077.418.063.62-.018.267-.11.518-.158.662l-1.138 3.416-.002.004-.049.142q.065-.02.14-.045.005 0 .01-.003l3.415-1.138c.136-.046.395-.139.663-.158l.159-.005q.155.002.308.03l.15.038.191.065c.184.076.345.175.443.231l.005.003c1.793 1.034 3.877 1.628 6.1 1.628 6.752 0 12.227-5.475 12.227-12.227S22.752 3.773 16 3.773" />
    </svg>
  );
}

function NextIcon() {
  return (
    <svg class="comments-icon CommentButton-open" viewBox="0 0 32 32" aria-hidden="true">
      <path d="M12.783 5.884c-.432-.432-1.134-.432-1.566 0s-.432 1.133 0 1.565L19.768 16l-8.55 8.55c-.433.433-.433 1.134 0 1.566.431.432 1.133.432 1.565 0l9.333-9.333c.208-.208.324-.49.324-.783s-.117-.575-.324-.783z" />
    </svg>
  );
}

/** A commenter's avatar source: their stored photo, or initials. */
export function commenterAvatar(c: Commenter): string | null {
  return c.photo ? avatarUrl(c.kind === 'user' ? 'users' : 'channels', c.id) : null;
}

/** "N 条评论" for a post's comment count; "发表评论" for none (Web A's LeaveAComment). */
export function commentsLabel(count: number): string {
  return count > 0 ? `${formatCount(count)} 条评论` : '发表评论';
}

/** The bar at the foot of a channel post (Web A's CommentButton): the newest commenters' avatars,
 * the count and an arrow, opening the post's comments. Stickers and round videos get the round
 * variant beside the bubble instead. A discussion the account cannot read is shown disabled. */
export function CommentButton({ chatId, postId, stats, customShape }: { chatId: number; postId: number; stats: PostStats; customShape?: boolean }) {
  const info = stats.comments;
  if (!info) return null;
  const count = stats.replies;
  const disabled = Boolean(info.unreadable);
  const recent = info.recent.slice(0, 3);
  const open = (e: Event) => {
    e.stopPropagation();
    if (!disabled) navigate({ name: 'comments', chatId, messageId: postId }, { fromChat: true });
  };
  return (
    <div
      class={`CommentButton${customShape ? ' custom-shape' : ''}${disabled ? ' disabled' : ''}`}
      data-cnt={formatCount(count)}
      role="button"
      tabIndex={disabled ? -1 : 0}
      aria-disabled={disabled}
      title={disabled ? '代取账号无法读取该频道的讨论组' : undefined}
      onClick={open}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') open(e);
      }}
    >
      <CommentsIcon />
      {recent.length === 0 && <MessageIcon />}
      {recent.length > 0 && (
        <div class="recent-repliers">
          {recent.map((c) => (
            <Avatar key={`${c.kind}:${c.id}`} name={c.name} peerId={c.id} src={commenterAvatar(c)} size="small" />
          ))}
        </div>
      )}
      <div class="label" dir="auto">
        {commentsLabel(count)}
      </div>
      <div class="CommentButton-right">
        <NextIcon />
      </div>
    </div>
  );
}
