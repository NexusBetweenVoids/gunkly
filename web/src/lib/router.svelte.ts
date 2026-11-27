import { BASE, href, unbase } from './base';

// Tiny history router: reactive `route.path` (prefix-free) plus link interception.
export const route = $state({ path: unbase(location.pathname) });

const emit = (): void => {
  route.path = unbase(location.pathname);
};

/** `to` is a prefix-free app path with optional query. */
export function navigate(to: string, replace = false): void {
  if (to === unbase(location.pathname) + location.search) return;
  history[replace ? 'replaceState' : 'pushState'](null, '', href(to));
  emit();
  window.scrollTo(0, 0);
}

addEventListener('popstate', emit);

// Intercept plain left-clicks on same-origin links that aren't API, downloads or new tabs.
document.addEventListener('click', (e) => {
  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
  const a = (e.target as Element | null)?.closest('a');
  if (!a || a.target || a.hasAttribute('download') || a.origin !== location.origin) return;
  if (BASE && a.pathname !== BASE && !a.pathname.startsWith(`${BASE}/`)) return; // another app on this host
  const p = unbase(a.pathname);
  if (p.startsWith('/api/') || p.startsWith('/oauth/')) return;
  e.preventDefault();
  navigate(p + a.search);
});
