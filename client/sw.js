// Service worker: lets the installed app open without a network connection,
// where "Same device" games still work. build.mjs copies this to dist/sw.js
// with the cache version and file list below filled in, so every build that
// changes a file gets a fresh cache and drops the old one.
const CACHE = "amoeba-{{version}}";
const PRECACHE = {{precache}};

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(CACHE)
      .then((cache) => cache.addAll(PRECACHE))
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  );
});

self.addEventListener("fetch", (event) => {
  const req = event.request;
  const url = new URL(req.url);
  if (req.method !== "GET" || url.origin !== location.origin || url.pathname === "/ws") return;

  if (req.mode === "navigate") {
    // Pages (/, /c/<code>, /r/<code>) come from the network when it's there,
    // so a new deploy shows up at once; offline they fall back to the cached
    // shell.
    event.respondWith(fetch(req).catch(() => caches.match("/")));
  } else if (url.pathname.startsWith("/assets/")) {
    // Hashed, never-changing files: the cache wins. Font subsets load on
    // demand, so they're added the first time they're used.
    event.respondWith(
      caches.match(req).then(
        (hit) =>
          hit ||
          fetch(req).then((res) => {
            if (res.ok) {
              const copy = res.clone();
              caches.open(CACHE).then((cache) => cache.put(req, copy));
            }
            return res;
          }),
      ),
    );
  } else {
    event.respondWith(fetch(req).catch(() => caches.match(req)));
  }
});
