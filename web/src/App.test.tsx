import { render } from '@testing-library/preact';
import { describe, expect, it } from 'vitest';
import { App } from './App';

describe('App shell', () => {
  it('renders the main container', () => {
    const { container } = render(<App />);
    expect(container.querySelector('#Main')).toBeTruthy();
  });
});
