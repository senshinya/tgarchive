import { useEffect } from 'preact/hooks';
import { ChatsPanel } from './components/left/ChatsPanel';
import { MiddleColumn } from './components/middle/MiddleColumn';
import { SharedMedia } from './components/right/SharedMedia';
import { SettingsPanel } from './components/settings/SettingsPanel';
import { MediaViewer } from './components/viewer/MediaViewer';
import { isSettings, route, startRouter } from './lib/router';
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
    );
    return () => {
      stopRouter();
      stopEvents();
    };
  }, []);

  const r = route.value;
  const chatId = r.name === 'chat' ? r.chatId : 0;
  const rightOpen = chatId > 0 && store.sharedMediaOpen.value;
  const cls = [!chatId && 'left-column-open', rightOpen && 'right-column-open'].filter(Boolean).join(' ');

  return (
    <StoreContext.Provider value={store}>
      <div id="Main" class={cls}>
        <div id="LeftColumn">{isSettings(r) ? <SettingsPanel route={r} /> : <ChatsPanel />}</div>
        <MiddleColumn chatId={chatId} />
        <div id="RightColumn" aria-hidden={!rightOpen}>
          {rightOpen && <SharedMedia chatId={chatId} />}
        </div>
      </div>
      <MediaViewer />
      <Toast />
    </StoreContext.Provider>
  );
}
