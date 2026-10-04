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
