'use strict';
// The Console's service worker: keeps the app shell so the installed app opens
// without a network, always trying the network first so the page is never
// stale. Live data (api/*) and anything off-origin is never touched.

const CACHE = 'berthier-shell-v1';
const scope = new URL(self.registration.scope);
const SHELL = ['./', 'manifest.webmanifest', 'icon-192.png', 'icon-512.png'].map((p) => new URL(p, scope).href);
const API = new URL('api/', scope).href;

self.addEventListener('install', (e) => {
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting()));
});

self.addEventListener('activate', (e) => {
  e.waitUntil(caches.keys()
    .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
    .then(() => self.clients.claim()));
});

self.addEventListener('fetch', (e) => {
  const req = e.request;
  if (req.method !== 'GET' || !req.url.startsWith(scope.origin + '/') || req.url.startsWith(API)) return;
  // Opening the page, whatever its query, is the page itself.
  const key = req.mode === 'navigate' && new URL(req.url).pathname === scope.pathname ? SHELL[0] : req.url;
  if (!SHELL.includes(key)) return;
  e.respondWith(fetch(req).then((res) => {
    if (res.ok) {
      const copy = res.clone();
      caches.open(CACHE).then((c) => c.put(key, copy));
    }
    return res;
  }).catch(() => caches.match(key).then((hit) => hit || Response.error())));
});
