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
   service `http://web:80`.
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
