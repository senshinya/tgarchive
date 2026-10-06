import { useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks';
import { convMedia, errorMessage } from '../../api/client';
import type { Message } from '../../api/types';
import { chatName } from '../../lib/format';
import { useStore, type ViewerItem, type ViewerTarget } from '../../state/store';
import { Spinner } from '../../ui/Spinner';
import { VISUAL_KINDS, displayKind, mainMedia, readyThumb } from '../media/util';
import './viewer.scss';

type GalleryModule = typeof import('./Gallery');
let loadedGallery: GalleryModule | null = null;
let galleryPromise: Promise<GalleryModule> | null = null;

/** PhotoSwipe + Vidstack live in their own chunk, fetched on the first open. */
function loadGallery(): Promise<GalleryModule> {
  galleryPromise ??= import('./Gallery').then(
    (m) => (loadedGallery = m),
    (err) => {
      galleryPromise = null; // let the next open retry
      throw err;
    },
  );
  return galleryPromise;
}

export const VIEWER_PAGE = 100;
export const VIEWER_MAX_PAGES = 50;

/** A fresh, per-open id for the history entry the viewer pushes (see ViewerInner below): unique
 * enough that it never collides with a stale `viewer` marker left on an older entry (e.g. from
 * before a reload, or from a previous open that didn't get cleaned up). */
function newViewerToken(): string {
  return typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `${Date.now()}-${Math.random()}`;
}

export function toViewerItems(msgs: Message[]): ViewerItem[] {
  const out: { id: number; item: ViewerItem }[] = [];
  for (const msg of msgs) {
    const media = mainMedia(msg);
    const kind = displayKind(msg);
    if (media && media.state === 'done' && VISUAL_KINDS.includes(kind)) {
      const thumb = readyThumb(msg);
      out.push({
        id: msg.id,
        item: {
          mediaId: media.id,
          kind,
          date: msg.date,
          text: msg.text,
          entities: msg.entities,
          chatId: msg.chat_id,
          thumbId: thumb?.id,
          width: media.width,
          height: media.height,
          mime: media.mime,
          compatCodec: media.compat_codec,
        },
      });
    }
  }
  return out.sort((a, b) => a.id - b.id).map((x) => x.item);
}

function ViewerInner({ target }: { target: ViewerTarget }) {
  const store = useStore();
  // One unique id per open, not a bare boolean: history.state survives reload/forward, so a
  // stale `viewer` marker can already sit on the entry this instance is about to push onto (or,
  // after that push, on the entry it pops back to). Comparing against this specific token — not
  // mere truthiness — means we only ever treat *our own* pushed entry as "the viewer is open",
  // so a stale leftover marker elsewhere in history can't make the first Back/X press a no-op.
  const tokenRef = useRef<string | undefined>(undefined);
  if (tokenRef.current === undefined) tokenRef.current = newViewerToken();
  const token = tokenRef.current;

  // Closing via UI: if the top history entry is the one we pushed on open (below), go back
  // through it instead of closing directly — the popstate handler below then closes the viewer,
  // keeping the hardware/system back button and these UI controls doing the same thing.
  const close = () => {
    if ((history.state as { viewer?: string } | null)?.viewer === token) history.back();
    else store.viewer.value = null;
  };

  // Opening the viewer pushes one history entry for the SAME url (preserving existing state
  // fields), so the system back button closes only the viewer instead of also leaving whatever
  // was underneath it (the article reader or the chat). Popping past that entry — i.e. a
  // popstate where the current entry's `viewer` marker is no longer this open's token — closes
  // the viewer without navigating further; the router re-parses the same path on this pop, which
  // is a no-op since the path never changed.
  // A layout effect, not useEffect: Preact defers useEffect to after the next paint
  // (requestAnimationFrame), so a back press right after opening — or any throttled rAF — would
  // pop the entry underneath (closing the reader too) before this one was ever pushed.
  useLayoutEffect(() => {
    history.pushState({ ...(history.state ?? {}), viewer: token }, '', location.href);
    const onPopState = () => {
      if ((history.state as { viewer?: string } | null)?.viewer !== token) store.viewer.value = null;
    };
    window.addEventListener('popstate', onPopState);
    return () => {
      window.removeEventListener('popstate', onPopState);
      // Closed some other way (e.g. store.viewer cleared elsewhere) while our entry is still on
      // top: drop it, or the next system back would land on a dead "viewer" entry.
      if ((history.state as { viewer?: string } | null)?.viewer === token) history.back();
    };
  }, []);
  const listed = 'list' in target ? target : null;
  const inChat = 'list' in target ? null : target;
  const chatId = inChat?.chatId ?? 0;
  const seed = listed ? listed.list : toViewerItems(store.conv(chatId).items.filter((m) => m.id === inChat?.messageId));
  const [items, setItems] = useState<ViewerItem[]>(seed);

  // Load the whole chat's media (newest first, paged by id) so left/right walks all of it. An
  // explicit list is already complete.
  useEffect(() => {
    if (listed) return;
    let cancelled = false;
    (async () => {
      const all: Message[] = [];
      let before = 0;
      for (let i = 0; i < VIEWER_MAX_PAGES; i++) {
        const page = await convMedia(store.api, chatId, 'media', before, VIEWER_PAGE);
        all.push(...page);
        if (page.length < VIEWER_PAGE) break;
        before = page[page.length - 1].id;
      }
      if (cancelled) return;
      const list = toViewerItems(all);
      if (list.some((it) => it.mediaId === target.mediaId)) setItems(list);
      else if (seed.length === 0) close();
    })().catch((err) => {
      if (cancelled) return;
      if (seed.length > 0) store.showToast(errorMessage(err));
      else close();
    });
    return () => {
      cancelled = true;
    };
  }, [chatId, target.mediaId]);

  const titleOf = (it: ViewerItem) => {
    if (listed) return listed.title;
    const chat = store.chats.value.find((c) => c.id === (it.chatId ?? chatId));
    return chat ? chatName(chat) : '';
  };

  const [gallery, setGallery] = useState<GalleryModule | null>(loadedGallery);
  useEffect(() => {
    if (gallery) return;
    let cancelled = false;
    loadGallery().then(
      (m) => !cancelled && setGallery(m),
      () => {
        if (cancelled) return;
        store.showToast('查看器加载失败');
        close();
      },
    );
    return () => {
      cancelled = true;
    };
  }, []);

  const [root, setRoot] = useState<HTMLDivElement | null>(null);
  const ready = gallery && root && items.some((it) => it.mediaId === target.mediaId);
  return (
    <div class="MediaViewer" role="dialog" aria-modal="true" aria-label="媒体查看器" ref={setRoot}>
      {ready ? (
        <gallery.Gallery items={items} mediaId={target.mediaId} container={root} titleOf={titleOf} onClosed={close} />
      ) : (
        <div class="MediaViewer-loading" aria-label="加载中">
          <Spinner size={48} />
        </div>
      )}
    </div>
  );
}

/** Full-screen viewer over all photos/videos/GIFs of the chat, or over an explicit list (an
 * article's media); opened by setting store.viewer. */
export function MediaViewer() {
  const store = useStore();
  const target = store.viewer.value;
  if (!target) return null;
  const key = 'list' in target ? `list:${target.mediaId}` : `${target.chatId}:${target.mediaId}`;
  return <ViewerInner key={key} target={target} />;
}
