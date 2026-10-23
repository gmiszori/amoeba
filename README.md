# amőba

Online five-in-a-row (Hungarian *amőba*, a.k.a. gomoku) on an infinite grid.
Pick a username, enter matchmaking, and you're paired with the next player
waiting. Blue ✕ always moves first; five or more in a row (horizontal,
vertical or diagonal) wins. Each turn has a 30 second clock: run out and you
lose. Built mobile-first so it plays well on phones and iPads.

## Architecture

```
Browser ─► [web: Caddy :80/:443, automatic HTTPS] ─┬─ static Astro build
                                                   └─ /ws ─► [server: Go :8080]
```

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

```bash
cp .env.example .env   # set DOMAIN=localhost for a local run
docker compose up --build
```

Open https://localhost (the certificate is self-signed locally, so the browser
warns once; use `DOMAIN=http://localhost` for plain HTTP instead). To play
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
2. Make sure inbound TCP 80 and 443 (and UDP 443 for HTTP/3) are open, and
   that nothing else on the server is already using ports 80/443.
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
| `DOMAIN` | `localhost` | Domain Caddy serves and gets a certificate for. |
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
  Linux capabilities and memory/process caps. Only Caddy is published; the
  game server is reachable only through it.

When deploying:

- Don't publish the server's port 8080: its per-IP limit trusts the
  `X-Forwarded-For` header that Caddy sets.
- Rebuild now and then (`docker compose build --pull`) to pick up base-image
  security fixes.
