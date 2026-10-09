import { render } from '@testing-library/preact';
import type { ComponentChildren } from 'preact';
import type { Api } from '../api/client';
import { createStore, StoreContext } from '../state/store';
import { fakeApi } from './fixtures';

/** Renders `ui` inside a StoreContext backed by a fake Api. */
export function renderWithStore(ui: ComponentChildren, api: Api = fakeApi()) {
  const store = createStore(api, { chatsReloadDelay: 0 });
  const result = render(<StoreContext.Provider value={store}>{ui}</StoreContext.Provider>);
  return { ...result, store, api };
}

/** Renders a conversation the way the app opens it: with the chat list loading alongside (a chat
 * waits for it to know its unread posts). */
export function renderOpen(ui: ComponentChildren, api: Api = fakeApi()) {
  const r = renderWithStore(ui, api);
  void r.store.loadChats();
  return r;
}
