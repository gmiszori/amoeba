# amőba

Online five-in-a-row (Hungarian *amőba*, a.k.a. gomoku) on an infinite grid.
Pick a username, enter matchmaking, and you're paired with the next player
waiting. Blue ✕ always moves first; five or more in a row (horizontal,
vertical or diagonal) wins. Each turn has a 30 second clock: run out and you
lose. Built mobile-first so it plays well on phones and iPads.

## Architecture

```
Browser ─► Cloudflare ─► [cloudflared] ─► [web: Caddy :80] ─┬─ static Astro build
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
cp .env.example .env
docker compose up --build
```

Open http://localhost:8080. To play against yourself, open a second browser
profile or a private window (tabs of the same browser share an identity).
To test from a phone on the same network, set `HTTP_BIND=0.0.0.0` in `.env`
and open `http://<your-computer-ip>:8080`.

### Development (hot reload)

```bash
docker compose -f docker-compose.dev.yml up --build --watch
```

Client changes reload in the browser instantly; server changes rebuild the
Go container.

### Tests

```bash
docker build --target test server
```

## Deploying on a VPS with Cloudflare Tunnel

1. In Cloudflare Zero Trust → Networks → Tunnels, create a tunnel and copy
   its token.
2. Add a public hostname for the tunnel (e.g. `amoeba.example.com`) with
   service `http://web:8080`.
3. On the VPS:
   ```bash
   git clone https://github.com/gmiszori/amoeba && cd amoeba
   cp .env.example .env
   # set CLOUDFLARE_TUNNEL_TOKEN=... and uncomment COMPOSE_PROFILES=tunnel
   docker compose up --build -d
   ```

No inbound ports need to be opened: Caddy only listens on `127.0.0.1` and
traffic reaches it through the tunnel. Restarting the server ends running
games (state is in memory).

## Configuration (`.env`)

| Variable | Default | |
|----------|---------|--|
| `TURN_SECONDS` | `30` | Seconds per move. |
| `HTTP_BIND` / `HTTP_PORT` | `127.0.0.1` / `8080` | Where Caddy is published on the host. |
| `COMPOSE_PROFILES` | – | `tunnel` starts cloudflared. |
| `CLOUDFLARE_TUNNEL_TOKEN` | – | Tunnel token. |
| `ALLOWED_ORIGINS` | – | Extra WebSocket origins, only needed if the page is served from a different host. |

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
- Every container runs as a non-root user with a read-only filesystem, no
  Linux capabilities and memory/process caps. Only Caddy is published, on
  `127.0.0.1`.

When deploying:

- Keep `HTTP_BIND=127.0.0.1`, so the only way in is the tunnel. The server
  trusts the `CF-Connecting-IP` header for its per-IP limit.
- In Cloudflare, consider a rate-limiting rule on `/ws` and "Always Use HTTPS".
- Never commit `.env`; it holds the tunnel token.
- Rebuild now and then (`docker compose build --pull`) to pick up base-image
  security fixes.
