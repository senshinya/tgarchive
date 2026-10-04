import { cleanup } from '@testing-library/preact';
import { afterEach } from 'vitest';

afterEach(() => cleanup());

// jsdom does not implement media playback or scrolling into view.
HTMLMediaElement.prototype.play = function play() {
  return Promise.resolve();
};
HTMLMediaElement.prototype.pause = function pause() {};
Element.prototype.scrollIntoView = function scrollIntoView() {};
