// @ts-check
import { defineConfig } from "astro/config";

export default defineConfig({
  output: "static",
  // The dev server runs behind Caddy (see docker-compose.dev.yml).
  server: { host: true, port: 4321 },
  vite: {
    server: { allowedHosts: true },
  },
});
