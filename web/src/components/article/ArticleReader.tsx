import { ArrowLeft } from 'lucide-preact';
import { useEffect, useState } from 'preact/hooks';
import { errorMessage } from '../../api/client';
import type { Article } from '../../api/types';
import { safeHref } from '../../lib/entities';
import { formatFullDate } from '../../lib/format';
import { navigate } from '../../lib/router';
import { useStore, type ViewerItem } from '../../state/store';
import { IconButton } from '../../ui/Button';
import { Spinner } from '../../ui/Spinner';
import { ArticleContent } from './ArticleContent';
import './article.scss';

/** Instant View style reader for an archived Telegraph article, opened by the article route. */
export function ArticleReader({ chatId, messageId }: { chatId: number; messageId: number }) {
  const store = useStore();
  const [article, setArticle] = useState<Article | null>(null);
  const [error, setError] = useState('');
  // Only for the header's title/link while the article itself is still loading.
  const msg = store.conv(chatId).items.find((m) => m.id === messageId);
  useEffect(() => {
    let cancelled = false;
    store.api.article(messageId).then(
      (a) => {
        if (cancelled) return;
        setArticle(a);
        setError('');
      },
      (e) => {
        if (!cancelled) setError(errorMessage(e));
      },
    );
    return () => {
      cancelled = true;
    };
  }, [messageId]);

  // Live updates come straight from SSE, not from the loaded conversation (a deep link to an
  // older message may never be loaded into one), debounced ~500ms so a burst of media.updated
  // (one per article image finishing) produces a single refetch rather than one per event.
  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const unsubscribe = store.onEvent((ev) => {
      const concerns =
        ev.type === 'resync' ||
        (ev.type === 'message.updated' && ev.data.message_id === messageId) ||
        (ev.type === 'media.updated' && (ev.data.message_ids ?? []).includes(messageId));
      if (!concerns) return;
      clearTimeout(timer);
      timer = setTimeout(() => {
        store.api.article(messageId).then(
          (a) => {
            if (!cancelled) {
              setArticle(a);
              setError('');
            }
          },
          (e) => {
            if (!cancelled) setError(errorMessage(e));
          },
        );
      }, 500);
    });
    return () => {
      cancelled = true;
      clearTimeout(timer);
      unsubscribe();
    };
  }, [messageId]);

  // Opened from the chat: go back through history, so the system back button and this one agree.
  // Deep link: there is nothing of ours to go back to, so replace the entry with the chat.
  const close = () => {
    const state = history.state as { fromChat?: boolean } | null;
    if (state?.fromChat) history.back();
    else navigate({ name: 'chat', chatId }, { replace: true });
  };

  const media = new Map((article?.media ?? []).map((m) => [m.id, m]));
  const viewable: ViewerItem[] = (article?.media ?? [])
    .filter((m) => m.state === 'done' && (m.kind === 'photo' || m.kind === 'video'))
    .map((m) => ({ mediaId: m.id, kind: m.kind, date: article!.fetched_at, text: '', entities: [] }));
  const openMedia = (mediaId: number) => {
    if (article) store.viewer.value = { list: viewable, mediaId, title: article.title };
  };
  const retry = async (mediaId: number) => {
    try {
      await store.api.retryMedia(mediaId);
      setArticle((a) => a && { ...a, media: a.media.map((m) => (m.id === mediaId ? { ...m, state: 'pending' as const } : m)) });
    } catch (e) {
      store.showToast(errorMessage(e));
    }
  };

  const title = article?.title || msg?.article?.title || '文章';
  const original = safeHref(article?.url ?? msg?.article?.url ?? '');
  const authorHref = article?.author_url ? safeHref(article.author_url) : null;

  return (
    <div class="ArticleReader" role="dialog" aria-modal="true" aria-label="文章">
      <div class="ArticleReader-header">
        <IconButton label="返回" onClick={close}>
          <ArrowLeft size={24} />
        </IconButton>
        <span class="ArticleReader-title">{title}</span>
        {original && (
          <a class="ArticleReader-open" href={original} target="_blank" rel="noopener noreferrer">
            在 Telegraph 打开
          </a>
        )}
      </div>
      <div class="ArticleReader-scroll">
        {article ? (
          <article class="ArticleReader-body">
            <h1 class="ArticleReader-heading">{article.title}</h1>
            <div class="ArticleReader-byline">
              {article.author_name &&
                (authorHref ? (
                  <a href={authorHref} target="_blank" rel="noopener noreferrer">
                    {article.author_name}
                  </a>
                ) : (
                  <span>{article.author_name}</span>
                ))}
              <span>存档于 {formatFullDate(article.fetched_at)}</span>
            </div>
            <ArticleContent nodes={article.content} media={media} onOpenMedia={openMedia} onRetry={retry} />
          </article>
        ) : (
          <div class="ArticleReader-state">{error || <Spinner />}</div>
        )}
      </div>
    </div>
  );
}
