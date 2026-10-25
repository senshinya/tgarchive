import type { LibraryVideo, VideoLibraryItem, VideoLibraryParams } from '../../api/types';
import { routePath, type Route } from '../../lib/router';
import { readyThumb } from '../media/util';

export type LibraryRoute = Extract<Route, { name: 'videos' | 'video-detail' | 'video-play' }>;
export function isLibraryRoute(r: Route): r is LibraryRoute {
  return r.name === 'videos' || r.name === 'video-detail' || r.name === 'video-play';
}
export function libraryParams(query = ''): VideoLibraryParams {
  const p = new URLSearchParams(query);
  const choice = <const T extends string>(key: string, options: readonly T[], fallback: T): T => {
    const value = p.get(key) as T;
    return options.includes(value) ? value : fallback;
  };
  const id = (key: string) => (/^[1-9]\d{0,14}$/.test(p.get(key) || '') ? Number(p.get(key)) : 0);
  return {
    q: (p.get('q') || '').slice(0, 200),
    chat_id: id('chat_id'),
    tag_id: id('tag_id'),
    view: choice('view', ['all', 'continue', 'collections', 'favorites'], 'all'),
    kind: choice('kind', ['all', 'single', 'collection'], 'all'),
    watched: choice('watched', ['all', 'unwatched', 'in_progress', 'completed'], 'all'),
    sort: choice('sort', ['new', 'old', 'published', 'duration', 'title'], 'new'),
  };
}
export function libraryQuery(params: VideoLibraryParams): string {
  const q = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value && value !== 'all' && value !== 'new' && key !== 'cursor' && key !== 'limit') q.set(key, String(value));
  }
  return q.toString();
}
export function resumeVideo(item: VideoLibraryItem): LibraryVideo {
  return (
    item.videos.find((v) => v.progress.position > 0 && !v.progress.completed && v.media.state === 'done') ||
    item.videos.find((v) => !v.progress.completed && v.media.state === 'done') ||
    item.videos.find((v) => v.media.state === 'done') ||
    item.videos[0]
  );
}
export function coverVideo(item: VideoLibraryItem): LibraryVideo | undefined {
  return item.videos.find((v) => v.media.id === item.cover_media_id) || item.videos[0];
}
export function coverThumb(video: LibraryVideo | undefined): number | undefined {
  return video && readyThumb(video.message)?.id;
}
export function videoTitle(v: LibraryVideo, index = 0): string {
  return v.media.file_name || v.message.text.split('\n').find((x) => x.trim()) || `视频 ${index + 1}`;
}
export function libraryHref(params: VideoLibraryParams): string {
  return routePath({ name: 'videos', query: libraryQuery(params) });
}
export const EMPTY_LIBRARY = {
  items: [],
  next: '',
  total_items: 0,
  total_videos: 0,
  collections: 0,
};

let lastQuery = '';
export function rememberLibraryQuery(query: string) {
  lastQuery = query;
}
export function libraryReturnRoute(): Route {
  return { name: 'videos', query: lastQuery };
}
