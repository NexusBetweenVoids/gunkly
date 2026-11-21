import { mount } from 'svelte';
import './app.css';
import App from './App.svelte';
import { applyLocale } from './lib/i18n';
import { applyTheme, storedTheme } from './lib/theme';

applyLocale();
applyTheme(storedTheme());

mount(App, { target: document.getElementById('app')! });

if (import.meta.env.PROD && 'serviceWorker' in navigator) {
  navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch(() => {});
}
