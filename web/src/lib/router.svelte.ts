// Tiny history router: reactive `route.path` plus link interception.
export const route = $state({ path: location.pathname });

const emit = (): void => {
  route.path = location.pathname;
};

export function navigate(to: string, replace = false): void {
  if (to === location.pathname + location.search) return;
  history[replace ? 'replaceState' : 'pushState'](null, '', to);
  emit();
  window.scrollTo(0, 0);
}

addEventListener('popstate', emit);

// Intercept plain left-clicks on same-origin links that aren't API, downloads or new tabs.
document.addEventListener('click', (e) => {
  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
  const a = (e.target as Element | null)?.closest('a');
  if (!a || a.target || a.hasAttribute('download') || a.origin !== location.origin) return;
  if (a.pathname.startsWith('/api/')) return;
  e.preventDefault();
  navigate(a.pathname + a.search);
});
