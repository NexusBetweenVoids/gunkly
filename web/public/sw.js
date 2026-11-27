// Gunkly service worker: installability and Web Push only. No fetch handler, no caching.
self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()));
// Path prefix this app is served under (sub-path hosting): the scope is <prefix>/. Push URLs are prefix-free app paths.
const ICON_PREFIX = new URL(self.registration.scope).pathname.replace(/\/$/, '');
// Payload {"title","body","url"}, already localised by the server.
self.addEventListener('push', (e) => {
  let d = {};
  try {
    d = e.data.json();
  } catch {}
  if (d.title) e.waitUntil(self.registration.showNotification(d.title, { body: d.body || '', icon: ICON_PREFIX + '/icons/icon-192.png', data: { url: d.url } }));
});
// Same-origin paths only: reject //host and /\host, then confirm by parsing.
function sameOriginUrl(raw) {
  const o = self.location.origin;
  const PREFIX = self.registration ? new URL(self.registration.scope).pathname.replace(/\/$/, '') : '';
  try {
    const u = typeof raw === 'string' && /^\/[^/\\]/.test(raw) ? new URL(PREFIX + raw, o) : null;
    if (u && u.origin === o) return u;
  } catch {}
  return new URL(PREFIX + '/', o);
}
self.addEventListener('notificationclick', (e) => {
  e.notification.close();
  const url = sameOriginUrl(e.notification.data && e.notification.data.url).href;
  e.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((l) => {
      const c = l.find((c) => c.url.startsWith(self.location.origin) && 'focus' in c);
      return c ? c.navigate(url).then((w) => (w || c).focus()) : self.clients.openWindow(url);
    }),
  );
});
