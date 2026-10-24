# WebSocket protocol

The client connects to `/ws` (same origin as the page) and exchanges JSON
text messages, each with a `type` field. Types live in
`server/internal/protocol/protocol.go` and are mirrored in
`client/src/scripts/protocol.ts`. Keep the two in sync.

The server is authoritative: it validates every move, runs the turn clock and
decides the winner. Clients only send intentions.

## Client → server

| type      | fields          | meaning |
|-----------|-----------------|---------|
| `hello`   | `token?`        | First message on every connection. Send the stored token to resume a session; omit it to get a new one. |
| `join`    | `name`          | Enter matchmaking with this username (1–16 chars: letters, digits, spaces, `_ - .`). Also leaves a finished game. |
| `challenge` | `name`        | Create a challenge link for a friend (answered with `challenge`). Asking again while waiting returns the same code. |
| `peek`    | `code`          | Look up a challenge code from a `/c/<code>` link (answered with `invite`). |
| `accept`  | `code`, `name`  | Accept a challenge: starts a game with whoever created it. |
| `cancel`  |                 | Leave the matchmaking queue, or withdraw your challenge link. |
| `move`    | `x`, `y`        | Place your mark at cell (x, y). Integers, \|x\|,\|y\| ≤ 1,000,000. |
| `rematch` |                 | Ask for (or accept) a rematch after a game ends. Colours swap. |
| `leave`   |                 | Back to the lobby. Resigns a running game. |

## Server → client

| type      | fields | meaning |
|-----------|--------|---------|
| `welcome` | `token`, `name` | Reply to `hello`. Store the token (local storage). |
| `lobby`   |        | Not queued, not in a game. |
| `queued`  |        | Waiting for an opponent. |
| `challenge` | `code` | Waiting for a friend to open `/c/<code>`. Also sent on reconnect while the link is still open. |
| `invite`  | `code`, `name` | Reply to `peek`: `name` is challenging you. |
| `game`    | see below | Full state of your current match. Sent whenever it changes. |
| `error`   | `code?`, `message` | A request was rejected. `code` is `name` when the username was invalid or blocked (see `server/internal/hub/badwords.txt`); the player is back in the lobby. `code` is `challenge` when a challenge link can't be used (expired, your own, or its owner is offline). |
| `kicked`  |        | The same session connected from elsewhere; this connection closes. Don't reconnect automatically. |
| `stats`   | `online` | Number of players in running games or waiting in the queue. Sent with `lobby`/`queued` and again (at most once a second) whenever it changes, to players not in a game. |

### `game`

```json
{
  "type": "game",
  "id": "4f1c…",
  "mode": "random",
  "you": "X",
  "yourName": "Gergő",
  "opponentName": "Bob",
  "opponentConnected": true,
  "moves": [[0, 0], [1, 0], [0, 1]],
  "turn": "O",
  "remainingMs": 28412,
  "turnMs": 30000,
  "over": false,
  "winner": "X",
  "reason": "five",
  "line": [[0, 0], [0, 1], [0, 2], [0, 3], [0, 4]],
  "rematch": { "you": false, "opponent": false },
  "opponentLeft": false
}
```

- `mode` is `random` (paired from the queue) or `friend` (from a challenge
  link). Games against Amőbot run entirely in the browser and never reach
  the server.
- `moves` are in play order; even indexes are X, odd are O. X (blue) always
  moves first.
- `remainingMs` is the time left in the current turn when the message was
  sent; the client counts down locally. 0 when the game is over.
- `winner`, `reason` (`five` | `timeout` | `resign`) and `line` appear only
  when `over` is true. `line` is only set for `five` and may be longer than 5.

## Rules and timing

- Five or more in a row (horizontal, vertical, diagonal) wins.
- Each turn lasts `TURN_SECONDS` (default 30). Running out of time loses.
- Disconnecting doesn't pause the clock: a player who drops has until the end
  of their turn to reconnect with their token and carry on.
- After a game ends, a player who leaves (back to the lobby or matchmaking,
  or disconnects and doesn't return within 3 s) is gone for good: the
  opponent gets `opponentLeft: true`, and a rematch is no longer possible.
  `rematch.you` stays set, so the client can say "rematch cancelled".
- A player in the queue who disconnects is removed from it (the client rejoins
  automatically on reconnect).
- A challenge link is single-use. It survives its owner's brief disconnects
  (it can't be accepted while they're away) and lasts until they start any
  game, cancel, or have been gone for 10 minutes.
- The server pings every 25 s to keep idle connections alive through
  proxies and mobile NATs.
