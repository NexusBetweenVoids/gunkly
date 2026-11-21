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

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  // Only open same-origin paths.
  const raw = event.notification.data && event.notification.data.url;
  const url = new URL(typeof raw === 'string' && raw.startsWith('/') && !raw.startsWith('//') ? raw : '/', self.location.origin);
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
