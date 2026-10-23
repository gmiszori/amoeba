// Builds the static site into dist/:
//   node build.mjs         production: minified bundles with content hashes
//   node build.mjs --dev   watch + serve on :4321, reloading the page on change
//
// index.html is copied with {{css}} and {{js}} replaced by the bundle paths;
// public/ is copied as is.
import * as esbuild from "esbuild";
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

async function writeHtml(metafile) {
  const url = (entry) => {
    const [out] = Object.entries(metafile.outputs).find(([, o]) => o.entryPoint === entry);
    return "/" + path.relative(outdir, out).split(path.sep).join("/");
  };
  let html = await readFile("index.html", "utf8");
  for (const [token, entry] of [["{{css}}", entries.css], ["{{js}}", entries.js]]) {
    if (html.split(token).length !== 2) throw new Error(`index.html must contain ${token} exactly once`);
    html = html.replace(token, url(entry));
  }
  await writeFile(`${outdir}/index.html`, html);
}

await rm(outdir, { recursive: true, force: true });
await cp("public", outdir, { recursive: true });

if (!dev) {
  const result = await esbuild.build(options);
  await writeHtml(result.metafile);
} else {
  const ctx = await esbuild.context({
    ...options,
    plugins: [
      {
        name: "html",
        setup(build) {
          build.onEnd((result) => result.metafile && writeHtml(result.metafile));
        },
      },
    ],
  });
  await ctx.watch();
  // index.html and public/ aren't part of the bundle graph; rebuild on edits.
  watch("index.html", () => ctx.rebuild());
  watch("public", { recursive: true }, () => cp("public", outdir, { recursive: true }));
  await ctx.serve({ host: "0.0.0.0", port: 4321, servedir: outdir });
}
