// Sub-path hosting: the server injects <meta name="gunkly-base" content="/gunkly"> into index.html
// when GUNKLY_BASE_URL has a path. App code works with prefix-free paths ("/settings"); these helpers
// add and remove the prefix at the edges (links, history, fetch, service worker). The plain script
// public/sw.js mirrors this logic; keep in sync.
export const BASE: string = (document.querySelector('meta[name="gunkly-base"]')?.getAttribute('content') ?? '').replace(/\/+$/, '');

/** App path ("/signin?x=1") to the URL path a browser must use. */
export const href = (p: string): string => BASE + p;

/** URL pathname to app path ("/gunkly/settings" -> "/settings"). */
export const unbase = (pathname: string): string => {
  if (BASE && (pathname === BASE || pathname.startsWith(`${BASE}/`))) return pathname.slice(BASE.length) || '/';
  return pathname;
};

/** The current location as an app path with its query. */
export const appPath = (): string => unbase(location.pathname) + location.search;

/** This instance's address: origin plus prefix, no trailing slash. */
export const appOrigin = (): string => location.origin + BASE;
