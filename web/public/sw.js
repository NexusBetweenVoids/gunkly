// Gunkly service worker. Enables installability and (later) Web Push.
// Deliberately no fetch handler and no caching: Gunkly has no offline mode,
// and API responses must never be cached.
self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()));

// Payload (from the server, already localised): {"title": "...", "body": "...", "url": "/..."}
self.addEventListener('push', (event) => {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch {
    // Ignore malformed payloads.
  }
  if (!data.title) return;
  event.waitUntil(
    self.registration.showNotification(data.title, {
      body: data.body || '',
      icon: '/icons/icon-192.png',
      data: { url: data.url },
    }),
  );
});

// Same-origin paths only: reject //host and /\host, then confirm by parsing.
function sameOriginUrl(raw) {
  const origin = self.location.origin;
  if (typeof raw !== 'string' || !raw.startsWith('/') || raw.startsWith('//') || raw.startsWith('/\\')) return new URL('/', origin);
  try {
    const u = new URL(raw, origin);
    return u.origin === origin ? u : new URL('/', origin);
  } catch {
    return new URL('/', origin);
  }
}

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  // Only open same-origin paths.
  const raw = event.notification.data && event.notification.data.url;
  const url = sameOriginUrl(raw);
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((list) => {
      for (const c of list) {
        if (c.url.startsWith(self.location.origin) && 'focus' in c) {
          return c.navigate(url.href).then((w) => (w || c).focus());
        }
      }
      return self.clients.openWindow(url.href);
    }),
  );
});
