// Builds the static site into dist/:
//   node build.mjs         production: minified bundles with content hashes
//   node build.mjs --dev   watch + serve on :4321, reloading the page on change
//
// index.html is copied with {{css}} and {{js}} replaced by the bundle paths;
// public/ is copied as is. Production builds also get the service worker,
// sw.js, with the list of files to keep offline.
import * as esbuild from "esbuild";
import { createHash } from "node:crypto";
import { cp, readFile, rm, writeFile } from "node:fs/promises";
import { watch } from "node:fs";
import path from "node:path";

const dev = process.argv.includes("--dev");
const outdir = "dist";
const entries = { js: "src/scripts/main.ts", css: "src/styles/global.css" };

const options = {
  entryPoints: Object.values(entries),
  outdir: `${outdir}/assets`,
  // Hashed names let Caddy cache /assets/* forever; dev keeps them stable.
  entryNames: dev ? "[name]" : "[name]-[hash]",
  bundle: true,
  format: "esm",
  target: "es2022",
  minify: !dev,
  sourcemap: dev,
  metafile: true,
  // Fonts imported by the CSS land next to the bundles, hashed the same way.
  loader: { ".woff2": "file", ".woff": "file" },
  assetNames: dev ? "[name]" : "[name]-[hash]",
  define: { DEV: String(dev) },
  logLevel: "info",
};

const bundleUrl = (metafile, entry) => {
  const [out] = Object.entries(metafile.outputs).find(([, o]) => o.entryPoint === entry);
  return "/" + path.relative(outdir, out).split(path.sep).join("/");
};

// Replaces each {{token}} in a template, which must appear exactly once.
async function fill(template, values) {
  let text = await readFile(template, "utf8");
  for (const [token, value] of Object.entries(values)) {
    if (text.split(`{{${token}}}`).length !== 2) throw new Error(`${template} must contain {{${token}}} exactly once`);
    text = text.replace(`{{${token}}}`, value);
  }
  return text;
}

async function writeHtml(metafile) {
  const html = await fill("index.html", {
    css: bundleUrl(metafile, entries.css),
    js: bundleUrl(metafile, entries.js),
  });
  await writeFile(`${outdir}/index.html`, html);
  return html;
}

// The service worker caches the page, the bundles and the app icons up
// front; its version changes whenever any of them does.
async function writeServiceWorker(metafile, html) {
  const precache = [
    "/",
    bundleUrl(metafile, entries.css),
    bundleUrl(metafile, entries.js),
    "/manifest.webmanifest",
    "/favicon.svg",
    "/icons/icon-192.png",
  ];
  const version = createHash("sha256").update(html).update(precache.join()).digest("hex").slice(0, 12);
  const sw = await fill("sw.js", { version, precache: JSON.stringify(precache) });
  await writeFile(`${outdir}/sw.js`, sw);
}

await rm(outdir, { recursive: true, force: true });
await cp("public", outdir, { recursive: true });

if (!dev) {
  const result = await esbuild.build(options);
  const html = await writeHtml(result.metafile);
  await writeServiceWorker(result.metafile, html);
} else {
  const ctx = await esbuild.context({
    ...options,
    plugins: [
      {
        name: "html",
        setup(build) {
          build.onEnd(async (result) => {
            if (result.metafile) await writeHtml(result.metafile);
          });
        },
      },
    ],
  });
  await ctx.watch();
  // index.html and public/ aren't part of the bundle graph; rebuild on edits.
  watch("index.html", () => ctx.rebuild());
  // Several files changing at once start overlapping copies that can trip
  // over each other; the last one still lands, so just report it.
  watch("public", { recursive: true }, () =>
    cp("public", outdir, { recursive: true }).catch((err) => console.warn(`copy public/: ${err.message}`)),
  );
  // Challenge links (/c/<code>) are the same page; anything without a file
  // gets index.html, like the Caddyfile's rewrite.
  await ctx.serve({ host: "0.0.0.0", port: 4321, servedir: outdir, fallback: `${outdir}/index.html` });
}
