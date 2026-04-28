// Argus service worker.
//
// Strategy:
//  - cache-first for the app shell (HTML, vendor JS/CSS, manifest, icons)
//  - network-only for /api/*
//  - push event handler for idle notifications
//
// Increment SW_VERSION on any shell change to bust the cache.

const SW_VERSION = 'argus-shell-v3';
const SHELL_ASSETS = [
  '/',
  '/manifest.webmanifest',
  '/vendor/xterm.js',
  '/vendor/xterm.css',
  '/vendor/xterm-addon-fit.js',
  '/icon-192.png',
  '/icon-512.png',
  '/apple-touch-icon.png',
];

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(SW_VERSION).then(cache => cache.addAll(SHELL_ASSETS))
      .then(() => self.skipWaiting())
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then(keys =>
      Promise.all(keys.filter(k => k !== SW_VERSION).map(k => caches.delete(k)))
    ).then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', (event) => {
  const url = new URL(event.request.url);

  // Same-origin only.
  if (url.origin !== self.location.origin) return;

  // /api/* — network only, never cache (auth, dynamic data).
  if (url.pathname.startsWith('/api/')) {
    return; // let the browser handle it normally
  }

  // Web Share Target lands at /share?title=...&text=...&url=...
  // Always serve the cached app shell so the SPA boots offline; the page-level
  // JS reads location.search to pull out the shared content.
  if (url.pathname === '/share' && event.request.method === 'GET') {
    event.respondWith(
      caches.match('/').then(cached => cached || fetch('/'))
    );
    return;
  }

  // App shell — cache-first, fall back to network.
  if (event.request.method === 'GET') {
    event.respondWith(
      caches.match(event.request).then(cached => {
        if (cached) {
          // Refresh in background.
          fetch(event.request).then(fresh => {
            if (fresh && fresh.ok) {
              caches.open(SW_VERSION).then(c => c.put(event.request, fresh.clone()));
            }
          }).catch(() => {});
          return cached;
        }
        return fetch(event.request).then(fresh => {
          if (fresh && fresh.ok && fresh.type === 'basic') {
            const clone = fresh.clone();
            caches.open(SW_VERSION).then(c => c.put(event.request, clone));
          }
          return fresh;
        });
      })
    );
  }
});

// Push events — Phase 5 wires this up server-side.
self.addEventListener('push', (event) => {
  let data = { title: 'Argus', body: 'Agent needs attention' };
  try {
    if (event.data) data = { ...data, ...event.data.json() };
  } catch (e) {}
  event.waitUntil(
    self.registration.showNotification(data.title, {
      body: data.body,
      icon: '/icon-192.png',
      badge: '/icon-192.png',
      data: { taskId: data.taskId },
      tag: data.taskId || 'argus',
    })
  );
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const taskId = event.notification.data?.taskId;
  const url = taskId ? `/?task=${encodeURIComponent(taskId)}` : '/';
  event.waitUntil(
    clients.matchAll({ type: 'window' }).then(list => {
      for (const c of list) {
        if (c.url.includes(self.location.origin)) {
          c.focus();
          c.navigate(url);
          return;
        }
      }
      return clients.openWindow(url);
    })
  );
});
