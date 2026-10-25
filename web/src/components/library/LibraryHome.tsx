import { ChevronRight, Film, History, Layers } from 'lucide-preact';
import type { ComponentChildren } from 'preact';
import { useLayoutEffect, useRef, useState } from 'preact/hooks';
import { type Api, errorMessage } from '../../api/client';
import type { TagCount, VideoLibraryItem, VideoLibraryPage } from '../../api/types';
import { chatName } from '../../lib/format';
import { navigate } from '../../lib/router';
import { useStore } from '../../state/store';
import { Spinner } from '../../ui/Spinner';
import { LibraryCard, LibraryLink } from './LibraryCard';
import { EMPTY_LIBRARY, libraryParams, libraryQuery, rememberLibraryQuery } from './library';

interface CachedLibrary {
  page: VideoLibraryPage;
  continuing: VideoLibraryItem[];
  collections: VideoLibraryItem[];
  scroll: number;
  at: number;
  revision: number;
}
const caches = new WeakMap<Api, Map<string, CachedLibrary>>();
let revision = 0;
export function invalidateLibraryCache() {
  revision++;
}
function cacheFor(api: Api) {
  let cache = caches.get(api);
  if (!cache) {
    cache = new Map<string, CachedLibrary>();
    caches.set(api, cache);
  }
  return cache;
}
export function LibrarySection({
  title,
  children,
  icon,
  more,
  class: cls = '',
}: {
  title: string;
  children: ComponentChildren;
  icon?: ComponentChildren;
  more?: ComponentChildren;
  class?: string;
}) {
  const grid = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const el = grid.current;
    if (!el || typeof ResizeObserver === 'undefined') return;
    const layout = () => {
      const style = getComputedStyle(el);
      const masonry = style.display === 'grid';
      const gap = parseFloat(style.getPropertyValue('--library-row-gap')) || 24;
      for (const card of Array.from(el.children) as HTMLElement[]) {
        card.style.gridRowEnd = masonry ? `span ${Math.ceil(card.getBoundingClientRect().height + gap)}` : '';
      }
    };
    const observer = new ResizeObserver(layout);
    observer.observe(el);
    for (const card of Array.from(el.children)) observer.observe(card);
    layout();
    return () => observer.disconnect();
  }, [children]);
  return (
    <section class="LibrarySection">
      <div class="LibrarySection-head">
        <h2>
          {icon}
          {title}
        </h2>
        {more}
      </div>
      <div class={`LibraryGrid ${cls}`} ref={grid}>
        {children}
      </div>
    </section>
  );
}
export function LibraryError({ text, retry }: { text: string; retry: () => void }) {
  return (
    <div class="LibraryNotice" role="alert">
      <p>{text}</p>
      <button class="LibraryButton secondary" onClick={retry}>
        重试
      </button>
    </div>
  );
}
export function LibraryHome({ query }: { query: string }) {
  const store = useStore();
  const params = libraryParams(query);
  const cache = cacheFor(store.api);
  const initial = cache.get(query);
  const [page, setPage] = useState<VideoLibraryPage>(initial?.page || EMPTY_LIBRARY);
  const [continuing, setContinuing] = useState<VideoLibraryItem[]>(initial?.continuing || []);
  const [collections, setCollections] = useState<VideoLibraryItem[]>(initial?.collections || []);
  const [tags, setTags] = useState<TagCount[]>([]);
  const [loading, setLoading] = useState(!initial);
  const [error, setError] = useState('');
  const [loaded, setLoaded] = useState(!!initial);
  const [newContent, setNewContent] = useState(false);
  const loadedRevision = useRef(initial?.revision ?? revision);
  const scroller = useRef<HTMLDivElement>(null);
  const scrollPosition = useRef(initial?.scroll || 0);
  const seq = useRef(0);
  const busy = useRef(false);
  const refreshPending = useRef(false);
  const notices = useRef(0);
  const alive = useRef(true);
  const snapshot = useRef({ page, continuing, collections, loaded, error });
  snapshot.current = { page, continuing, collections, loaded, error };
  const home = !query || Object.values(params).every((v) => !v || v === 'all' || v === 'new');
  const load = async (more = false) => {
    if (busy.current) { if (!more) refreshPending.current = true; return; }
    const noticeAtStart = notices.current;
    const generation = ++seq.current;
    busy.current = true;
    setLoading(true);
    setError('');
    try {
      const [result, resume, groups] = await Promise.all([
        store.api.videoLibrary({
          ...params,
          cursor: more ? snapshot.current.page.next : undefined,
          limit: 24,
        }),
        home && !more ? store.api.videoLibrary({ view: 'continue', limit: 6 }) : null,
        home && !more ? store.api.videoLibrary({ view: 'collections', limit: 4 }) : null,
      ]);
      // Revalidate the full range already browsed, so returning from detail keeps later pages.
      const previousCount = more ? 0 : snapshot.current.page.items.length;
      while (result.next && result.items.length < previousCount) {
        const following = await store.api.videoLibrary({
          ...params,
          cursor: result.next,
          limit: 24,
        });
        if (!alive.current || generation !== seq.current) return;
        result.items = [...result.items, ...following.items];
        result.next = following.next;
      }
      if (!alive.current || generation !== seq.current || refreshPending.current) return;
      setPage(
        more
          ? {
              ...result,
              items: [...snapshot.current.page.items, ...result.items].filter((it, i, all) => all.findIndex((x) => x.id === it.id) === i),
            }
          : result,
      );
      if (resume) setContinuing(resume.items);
      if (groups) setCollections(groups.items);
      loadedRevision.current = revision;
      setLoaded(true);
      if (notices.current === noticeAtStart) setNewContent(false);
    } catch (e) {
      if (alive.current && generation === seq.current) setError(errorMessage(e));
    } finally {
      if (alive.current && generation === seq.current) {
        busy.current = false;
        setLoading(false);
        if (refreshPending.current) { refreshPending.current = false; void load(); }
      }
    }
  };
  useLayoutEffect(() => {
    alive.current = true;
    rememberLibraryQuery(query);
    if (!initial || initial.revision !== revision || Date.now() - initial.at > 60000) void load();
    store.api.tags().then(
      (t) => {
        if (alive.current) setTags(t);
      },
      () => {},
    );
    const off = store.onEvent((ev) => {
      if (ev.type === 'resync' || ev.type === 'media.updated' || ev.type === 'message.deleted' || ev.type === 'favorites.updated')
        void load();
      else if (ev.type === 'message.created' || ev.type === 'message.updated' || ev.type === 'video-library.updated') { notices.current++; setNewContent(true); }
    });
    const focus = () => {
      if (document.visibilityState === 'visible') { notices.current++; setNewContent(true); }
    };
    document.addEventListener('visibilitychange', focus);
    const poll = setInterval(() => {
      if (document.visibilityState === 'visible') { notices.current++; setNewContent(true); }
    }, 60000);
    return () => {
      alive.current = false;
      seq.current++;
      off();
      clearInterval(poll);
      document.removeEventListener('visibilitychange', focus);
      if (snapshot.current.loaded) cache.set(query, {
        ...snapshot.current,
        scroll: scrollPosition.current,
        at: snapshot.current.error ? 0 : Date.now(),
        revision: loadedRevision.current,
      });
      if (cache.size > 20) cache.delete(cache.keys().next().value!);
    };
  }, []);
  useLayoutEffect(() => {
    if (loaded && initial && scroller.current) scroller.current.scrollTop = initial.scroll;
  }, [loaded]);
  const change = (patch: Partial<typeof params>) => navigate({ name: 'videos', query: libraryQuery({ ...params, ...patch }) });
  const favorite = async (item: VideoLibraryItem) => {
    try {
      if (item.message.favorite) await store.api.unfavorite(item.message.id);
      else await store.api.favorite(item.message.id);
      cache.clear();
      await load();
    } catch (e) {
      store.showToast(errorMessage(e));
    }
  };
  const views = {
    all: '视频库',
    continue: '继续观看',
    collections: '帖子合集',
    favorites: '我的收藏',
  };
  const title = params.q
    ? '搜索结果'
    : params.chat_id
      ? store.chats.value.find((c) => c.id === params.chat_id)
        ? chatName(store.chats.value.find((c) => c.id === params.chat_id)!)
        : '来源会话'
      : views[params.view || 'all'];
  const continuingVideos = continuing
    .flatMap((item) => item.videos.filter((v) => !v.progress.completed && v.progress.position > 0).map((video) => ({ item, video })))
    .slice(0, 3);
  const cards = page.items.map((item) => <LibraryCard key={item.id} item={item} onFavorite={(it) => void favorite(it)} />);
  return (
    <div
      class="LibraryScroll custom-scroll"
      ref={scroller}
      onScroll={() => {
        const el = scroller.current;
        if (el) scrollPosition.current = el.scrollTop;
        if (el && !loading && page.next && el.scrollHeight - el.scrollTop - el.clientHeight < 500) void load(true);
      }}
    >
      <div class="LibraryContent">
        <div class="LibraryHeading">
          <div>
            <h1>{title}</h1>
            <p>{loaded ? `${page.total_videos} 个视频 · ${page.collections} 个帖子合集` : '正在读取视频库…'}</p>
          </div>
          <span class="LibraryHeading-note">个人视频存档</span>
        </div>
        <div class="LibraryToolbar">
          <div class="LibraryChips">
            {(['all', 'single', 'collection'] as const).map((kind, i) => (
              <button
                type="button"
                key={kind}
                class={params.kind === kind ? 'active' : ''}
                aria-pressed={params.kind === kind}
                onClick={() => change({ kind })}
              >
                {['全部内容', '单视频', '合集'][i]}
              </button>
            ))}
          </div>
          <div class="LibraryFilters">
            <select aria-label="筛选来源" value={params.chat_id || 0} onChange={(e) => change({ chat_id: Number(e.currentTarget.value) })}>
              <option value={0}>全部来源</option>
              {store.chats.value.map((c) => (
                <option key={c.id} value={c.id}>
                  {chatName(c)}
                </option>
              ))}
            </select>
            <select
              aria-label="观看状态"
              value={params.watched}
              onChange={(e) =>
                change({
                  watched: e.currentTarget.value as typeof params.watched,
                })
              }
            >
              <option value="all">全部观看状态</option>
              <option value="unwatched">未看</option>
              <option value="in_progress">观看中</option>
              <option value="completed">已看</option>
            </select>
            {tags.length > 0 && (
              <select aria-label="筛选标签" value={params.tag_id || 0} onChange={(e) => change({ tag_id: Number(e.currentTarget.value) })}>
                <option value={0}>全部标签</option>
                {tags.map((t) => (
                  <option key={t.id} value={t.id}>
                    {t.name}
                  </option>
                ))}
              </select>
            )}
            <select
              aria-label="排序方式"
              value={params.sort}
              onChange={(e) => change({ sort: e.currentTarget.value as typeof params.sort })}
            >
              <option value="new">最近入库</option>
              <option value="old">最早入库</option>
              <option value="published">最近发布</option>
              <option value="duration">时长最长</option>
              <option value="title">标题排序</option>
            </select>
          </div>
        </div>
        {newContent && (
          <button class="LibraryRefresh" onClick={() => void load()}>
            有新内容或观看记录，点击刷新
          </button>
        )}
        {home && continuingVideos.length > 0 && (
          <LibrarySection
            title="继续观看"
            icon={<History size={19} />}
            class="resume"
            more={
              <LibraryLink to={{ name: 'videos', query: 'view=continue' }} class="LibraryMore">
                查看全部
                <ChevronRight size={15} />
              </LibraryLink>
            }
          >
            {continuingVideos.map(({ item, video }) => (
              <LibraryCard key={`${item.id}:${video.media.id}`} item={item} video={video} onFavorite={(it) => void favorite(it)} />
            ))}
          </LibrarySection>
        )}
        {error && <LibraryError text={error} retry={() => void load()} />}
        {loaded && !page.items.length && !error ? (
          <div class="LibraryNotice">
            <Film size={38} />
            <h2>{params.q ? '没有找到匹配的视频' : '这里还没有视频'}</h2>
            <p>{query ? '试试其他关键词，或清除筛选条件。' : '存档的视频会出现在这里，同帖多个视频将自动归为合集。'}</p>
            {query && (
              <button class="LibraryButton secondary" onClick={() => navigate({ name: 'videos' })}>
                清除筛选
              </button>
            )}
          </div>
        ) : (
          page.items.length > 0 && (
            <LibrarySection title={home ? '最近入库' : params.view === 'continue' ? '接着看' : `${page.total_items} 项内容`}>
              {params.view === 'continue'
                ? page.items.flatMap((item) =>
                    item.videos
                      .filter((v) => v.progress.position > 0 && !v.progress.completed)
                      .map((video) => (
                        <LibraryCard
                          key={`${item.id}:${video.media.id}`}
                          item={item}
                          video={video}
                          onFavorite={(it) => void favorite(it)}
                        />
                      )),
                  )
                : cards}
            </LibrarySection>
          )
        )}
        {loading && (
          <div class="LibraryNotice">
            <Spinner size={28} />
          </div>
        )}
        {!loading && page.next && (
          <button class="LibraryButton secondary LibraryLoadMore" onClick={() => void load(true)}>
            加载更多
          </button>
        )}
        {home && collections.length > 0 && (
          <LibrarySection
            title="帖子合集"
            icon={<Layers size={19} />}
            more={
              <LibraryLink to={{ name: 'videos', query: 'view=collections' }} class="LibraryMore">
                全部合集
                <ChevronRight size={15} />
              </LibraryLink>
            }
          >
            {collections.map((item) => (
              <LibraryCard key={item.id} item={item} onFavorite={(it) => void favorite(it)} />
            ))}
          </LibrarySection>
        )}
        <footer class="LibraryFooter">
          <span>仅展示视频 · 同帖多视频自动归为合集</span>
          <span>观看进度与消息已读独立保存</span>
        </footer>
      </div>
    </div>
  );
}
