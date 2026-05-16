// Argus service worker.
//
// Strategy:
//  - cache-first for the app shell (HTML, vendor JS/CSS, manifest, icons)
//  - network-only for /api/*
//  - push event handler for idle notifications
//
// Increment SW_VERSION on any shell change to bust the cache.

const SW_VERSION = 'argus-shell-v42';
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
  event.waitUntil((async () => {
    const list = await clients.matchAll({ type: 'window', includeUncontrolled: true });
    for (const c of list) {
      let cu;
      try { cu = new URL(c.url); } catch (e) { continue; }
      if (cu.origin !== self.location.origin) continue;
      // Existing client wins. Don't c.navigate — Safari/iOS support is
      // patchy and a full reload throws away the SPA's terminal/SSE state.
      // Send the SPA a postMessage instead; if focus() succeeds the user
      // lands on the deep-linked task with the existing app state intact.
      try { await c.focus(); } catch (e) {}
      // Message type must match the listener in static/index.html.
      if (taskId) c.postMessage({ type: 'argus:openTask', taskId });
      return;
    }
    // No existing client. Open a fresh window with the deep link encoded
    // in the URL — the SPA's load-time hook reads ?task= and opens it
    // once tasks have loaded.
    return clients.openWindow(url);
  })());
});
