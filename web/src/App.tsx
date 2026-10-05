import { useEffect, useRef } from 'preact/hooks';
import { NETWORK_ERROR } from './api/client';
import { ChatsPanel } from './components/left/ChatsPanel';
import { MiddleColumn } from './components/middle/MiddleColumn';
import { SharedMedia } from './components/right/SharedMedia';
import { SettingsPanel } from './components/settings/SettingsPanel';
import { MediaViewer } from './components/viewer/MediaViewer';
import { isSettings, navigate, route, routeChatId, routePath, startRouter } from './lib/router';
import { connectEvents, type EventSourceFactory } from './lib/sse';
import { StoreContext, type Store } from './state/store';
import { Toast } from './ui/Toast';
import './layout.scss';

interface Props {
  store: Store;
  /** Injected in tests; defaults to the browser EventSource. */
  eventSource?: EventSourceFactory;
}

export function App({ store, eventSource }: Props) {
  useEffect(() => {
    const stopRouter = startRouter();
    void store.loadBots();
    void store.loadChats();
    const stopEvents = connectEvents(
      (ev) => void store.handleEvent(ev),
      () => void store.resync(),
      eventSource,
      () => store.showToast(NETWORK_ERROR),
    );
    return () => {
      stopRouter();
      stopEvents();
    };
  }, []);

  const r = route.value;
  const chatId = routeChatId(r);
  const articleId = r.name === 'article' ? r.messageId : 0;
  const rightOpen = chatId > 0 && store.sharedMediaOpen.value;
  const cls = [!chatId && 'left-column-open', rightOpen && 'right-column-open'].filter(Boolean).join(' ');

  // Remembers the chat that was open before navigating away (e.g. into settings), so the
  // ≤925px left-overlay scrim can offer a way back to it. Updated during render, not an
  // effect: the value must be ready for the SAME render that may show the scrim.
  const lastChatId = useRef(0);
  if (chatId > 0) lastChatId.current = chatId;
  const showScrim = !chatId && lastChatId.current > 0;

  // Closing the viewer / shared-media panel on navigation keeps them from outliving the
  // content they were opened for (browser back, switching chats, etc). Keyed on the path, not
  // the route object: every popstate re-parses a fresh object, including the same-path pop that
  // backs out of the viewer's own history entry, and closing here on those would race the
  // viewer's popstate handling (a re-opened viewer could be closed by a late effect).
  const path = routePath(r);
  useEffect(() => {
    store.viewer.value = null;
  }, [path]);
  useEffect(() => {
    store.sharedMediaOpen.value = false;
  }, [chatId]);

  return (
    <StoreContext.Provider value={store}>
      <div id="Main" class={cls}>
        <div id="LeftColumn">{isSettings(r) ? <SettingsPanel route={r} /> : <ChatsPanel />}</div>
        {showScrim && (
          <button
            type="button"
            id="LeftScrim"
            aria-label="返回会话"
            onClick={() => navigate({ name: 'chat', chatId: lastChatId.current })}
          />
        )}
        <MiddleColumn chatId={chatId} articleId={articleId} />
        <div id="RightColumn" aria-hidden={!rightOpen}>
          {rightOpen && <SharedMedia key={chatId} chatId={chatId} />}
        </div>
      </div>
      <MediaViewer />
      <Toast />
    </StoreContext.Provider>
  );
}
