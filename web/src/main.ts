import { mount } from 'svelte';
import './app.css';
import App from './App.svelte';
import { href } from './lib/base';
import { applyLocale, loadMessages } from './lib/i18n';
import { applyTheme, storedTheme } from './lib/theme';

applyLocale();
applyTheme(storedTheme());

void loadMessages('core').then(() => mount(App, { target: document.getElementById('app')! }));

if (import.meta.env.PROD && 'serviceWorker' in navigator) {
  navigator.serviceWorker.register(href('/sw.js'), { scope: href('/') }).catch(() => {});
}
