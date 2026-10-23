# amőba

Online five-in-a-row (Hungarian *amőba*, a.k.a. gomoku) on an infinite grid.
Pick a username, enter matchmaking, and you're paired with the next player
waiting. Blue ✕ always moves first; five or more in a row (horizontal,
vertical or diagonal) wins. Each turn has a 30 second clock: run out and you
lose. Built mobile-first so it plays well on phones and iPads.

The home screen shows how many players are in a game right now, and
"Play on this device" starts a local game for two people sharing one screen
(no username, no timer, no server involved).

## Architecture

```
Browser ─► [webserver proxy :80/:443] ─► [web: Caddy, automatic HTTPS] ─┬─ static Astro build
                                                                        └─ /ws ─► [server: Go :8080]
```

Host ports 80 and 443 belong to the shared proxy in
[webserver](https://github.com/gmiszori/webserver), so several sites can run
on one server. It passes TLS through untouched; this repo's Caddy still holds
the certificate.

| Directory | What |
|-----------|------|
| `server/` | Go WebSocket server: sessions, matchmaking queue, game rules, turn clock. All state in memory; the server is authoritative. |
| `client/` | Astro static site; the game is a plain TypeScript canvas app. Also holds the `Caddyfile`; the production image is Caddy serving the build. |
| `docs/protocol.md` | The WebSocket message protocol. |

Usernames are 1–16 characters and are checked against an English and
Hungarian bad-word list in `server/internal/hub/badwords.txt`. It's plain
text, so add words there and rebuild the server.

Everything runs in Docker; you don't need Go or Node installed.

## Running

For day-to-day work use the [development stack](#development-hot-reload). To
run the production stack locally, start the webserver proxy first, with
`PUBLIC_BIND_IP=127.0.0.1` and `AMOEBA_DOMAIN=localhost` in its `.env`. Then:

```bash
cp .env.example .env   # set DOMAIN=localhost for a local run
docker compose up --build
```

Open https://localhost (the certificate is self-signed locally, so the browser
warns once). To play
against yourself, open a second browser profile or a private window (tabs of
the same browser share an identity).

### Development (hot reload)

```bash
docker compose -f docker-compose.dev.yml up --build --watch
```

Open http://localhost:8080. Client changes reload in the browser instantly;
server changes rebuild the Go container. To test from a phone on the same
network, set `HTTP_BIND=0.0.0.0` in `.env` and open
`http://<your-computer-ip>:8080`. The dev stack and the production stack can
run at the same time.

### Tests

```bash
docker build --target test server
```

## Deploying on a VPS

1. Point your domain's DNS `A` (and `AAAA`, if the server has IPv6) record at
   the server.
2. Make sure inbound TCP 80 and 443 are open, and that the
   [webserver](https://github.com/gmiszori/webserver) proxy is running with
   `AMOEBA_DOMAIN` set to your domain.
3. On the server:
   ```bash
   git clone https://github.com/gmiszori/amoeba && cd amoeba
   cp .env.example .env
   # set DOMAIN=your.domain
   docker compose up --build -d
   ```

Caddy requests a Let's Encrypt certificate on first start (check with
`docker compose logs web`) and renews it automatically. Certificates live in
the `caddy_data` volume, so they survive rebuilds. To update:
`git pull && docker compose up --build -d`. Restarting the server ends running
games (state is in memory).

If the domain's DNS is on Cloudflare, use "DNS only" (grey cloud). With the
orange-cloud proxy every player would appear to come from a Cloudflare
address, and they'd all share one per-IP connection limit.

## Configuration (`.env`)

| Variable | Default | |
|----------|---------|--|
| `DOMAIN` | `localhost` | Domain Caddy serves and gets a certificate for. Must match `AMOEBA_DOMAIN` in the webserver proxy. |
| `TURN_SECONDS` | `30` | Seconds per move. |
| `ALLOWED_ORIGINS` | – | Extra WebSocket origins, only needed if the page is served from a different host. |
| `HTTP_BIND` / `HTTP_PORT` | `127.0.0.1` / `8080` | Dev stack only: where its Caddy is published. |

## Security

What's in place:

- The server decides every move, the clock and the winner; clients can't cheat
  by editing the page. Session tokens are 128-bit random values.
- WebSockets only accept the site's own origin, messages are capped at 1 KB
  and 10/s per connection, and connections (50 per IP, 5000 total), sessions
  and moves per game are limited.
- Caddy sends a strict Content-Security-Policy (only the site's own scripts,
  styles and WebSocket), HSTS, and headers that stop other sites from framing
  the game.
- HTTPS everywhere: Caddy redirects HTTP to HTTPS and manages certificates.
- Every container runs as a non-root user with a read-only filesystem, no
  Linux capabilities and memory/process caps. Nothing is published on the
  host. Caddy is reachable only through the webserver proxy, and the game
  server only through Caddy.
- Caddy trusts the client address in a PROXY header only from the proxy's
  fixed address (`172.31.250.2`), so other containers can't fake one.

When deploying:

- Don't publish the server's port 8080: its per-IP limit trusts the
  `X-Forwarded-For` header that Caddy sets.
- Rebuild now and then (`docker compose build --pull`) to pick up base-image
  security fixes.
