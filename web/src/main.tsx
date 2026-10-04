import { render } from 'preact';
import { api } from './api/client';
import { App } from './App';
import { createStore } from './state/store';
import './styles/index.scss';

const ua = navigator.userAgent;
const ios = /iPhone|iPad/.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1);
if (/Macintosh|iPhone|iPad/.test(ua)) document.documentElement.classList.add('is-apple');
if (ios) document.documentElement.classList.add('is-ios');

render(<App store={createStore(api)} />, document.getElementById('app')!);
