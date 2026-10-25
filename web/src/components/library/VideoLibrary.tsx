import { ArrowLeft, Bookmark, Film, History, Layers, Search } from 'lucide-preact';
import { useEffect, useState } from 'preact/hooks';
import { chatName } from '../../lib/format';
import { navigate } from '../../lib/router';
import { useStore } from '../../state/store';
import { LibraryLink } from './LibraryCard';
import { LibraryHome } from './LibraryHome';
import { LibraryDetail } from './LibraryDetail';
import { LibraryPlayer } from './LibraryPlayer';
import { libraryParams, libraryQuery, type LibraryRoute } from './library';
import './library.scss';

const NAV = [
  { view: 'all', label: '视频库', icon: Film },
  { view: 'continue', label: '继续观看', icon: History },
  { view: 'collections', label: '帖子合集', icon: Layers },
  { view: 'favorites', label: '我的收藏', icon: Bookmark },
] as const;
/** A dedicated media workspace alongside the existing chat and media-wall modes. */
export function VideoLibrary({ current }: { current: LibraryRoute }) {
  const store = useStore();
  const params = libraryParams(current.name === 'videos' ? current.query : '');
  const [search, setSearch] = useState(params.q || '');
  useEffect(() => {
    setSearch(params.q || '');
  }, [params.q]);
  useEffect(() => {
    if (current.name !== 'videos' || search === (params.q || '')) return;
    const timer = setTimeout(() => navigate({ name: 'videos', query: libraryQuery({ ...params, q: search }) }, { replace: true }), 250);
    return () => clearTimeout(timer);
  }, [search, current]);
  const nav = NAV.map((n) => (
    <LibraryLink
      key={n.view}
      label={n.label} class={`LibraryNav-link${current.name === 'videos' && params.view === n.view && !params.chat_id ? ' active' : ''}`}
      to={{ name: 'videos', query: libraryQuery({ view: n.view }) }}
    >
      <n.icon size={20} />
      <span>{n.label}</span>
    </LibraryLink>
  ));
  return (
    <div class="VideoLibrary">
      <aside class="LibraryNav">
        <LibraryLink class="LibraryBrand" to={{ name: 'videos' }}>
          <span>
            <Film size={21} />
          </span>
          TGArchive
        </LibraryLink>
        <span class="LibraryNav-label">个人媒体</span>
        <nav aria-label="视频库导航">{nav}</nav>
        <div class="LibraryNav-sources">
          <span class="LibraryNav-label">来源会话</span>
          <nav aria-label="视频来源">
            {store.chats.value.slice(0, 6).map((c) => (
              <LibraryLink
                key={c.id}
                class={`LibraryNav-link${params.chat_id === c.id ? ' active' : ''}`}
                to={{ name: 'videos', query: libraryQuery({ chat_id: c.id }) }}
              >
                <span class="LibrarySource-icon">{chatName(c).slice(0, 1)}</span>
                <span>{chatName(c)}</span>
              </LibraryLink>
            ))}
          </nav>
        </div>
        <LibraryLink class="LibraryNav-exit" to={{ name: 'home' }}>
          <ArrowLeft size={18} />
          返回消息存档
        </LibraryLink>
      </aside>
      <section class="LibraryPanel" aria-label="视频库">
        <header class={`LibraryHeader${current.name === 'videos' ? ' has-search' : ''}`}>
          <span class="LibraryHeader-title">
            TGArchive <span>/</span> {current.name === 'videos' ? '视频库' : current.name === 'video-detail' ? '内容详情' : '正在观看'}
          </span>
          {current.name === 'videos' && (
            <label class="LibrarySearch">
              <Search size={18} />
              <input
                type="search"
                aria-label="搜索视频"
                placeholder="搜索视频、合集、帖子说明"
                value={search}
                maxLength={200}
                onInput={(e) => setSearch(e.currentTarget.value)}
              />
            </label>
          )}
        </header>
        {current.name === 'videos' ? (
          <LibraryHome key={current.query || ''} query={current.query || ''} />
        ) : current.name === 'video-detail' ? (
          <LibraryDetail key={current.itemId} itemId={current.itemId} />
        ) : (
          <LibraryPlayer key={`${current.itemId}:${current.mediaId}`} itemId={current.itemId} mediaId={current.mediaId} />
        )}
      </section>
      <nav class="LibraryMobileNav" aria-label="视频库移动导航">
        {nav}
        <LibraryLink to={{ name: 'home' }} class="LibraryNav-link">
          <ArrowLeft size={19} />
          <span>消息</span>
        </LibraryLink>
      </nav>
    </div>
  );
}
