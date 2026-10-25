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
| `room`    | `name`          | Open a group game (3–4 players) as its host (answered with `room`). Asking again while hosting returns the same room. |
| `peekRoom` | `code`         | Look up a group game from a `/r/<code>` link (answered with `roomInvite`). |
| `enter`   | `code`, `name`  | Join a group game's waiting room. The game starts by itself when the 4th player enters. |
| `start`   |                 | Host only: start the group game with the players in the room (at least 3). |
| `cancel`  |                 | Leave the matchmaking queue or a group game's waiting room, or withdraw your challenge link. When the host leaves, the room closes. |
| `move`    | `x`, `y`        | Place your mark at cell (x, y). Integers, \|x\|,\|y\| ≤ 1,000,000. |
| `rematch` |                 | Ask for (or accept) a rematch after a game ends. Colours swap. |
| `leave`   |                 | Back to the lobby. Resigns a running game (with 3 or more players, the others play on). |

## Server → client

| type      | fields | meaning |
|-----------|--------|---------|
| `welcome` | `token`, `name` | Reply to `hello`. Store the token (local storage). |
| `lobby`   |        | Not queued, not in a game. |
| `queued`  |        | Waiting for an opponent. |
| `challenge` | `code` | Waiting for a friend to open `/c/<code>`. Also sent on reconnect while the link is still open. |
| `invite`  | `code`, `name` | Reply to `peek`: `name` is challenging you. |
| `room`    | `code`, `players`, `you`, `max`, `min` | You're in a group game's waiting room. `players` is `[{name, connected}]` in joining order, the first is the host; `you` indexes it. Sent whenever the room changes and on reconnect. |
| `roomInvite` | `code`, `players`, `max` | Reply to `peekRoom`: the names of the players already in the room, host first. |
| `game`    | see below | Full state of your current match. Sent whenever it changes. |
| `error`   | `code?`, `message` | A request was rejected. `code` is `name` when the username was invalid or blocked (see `server/internal/hub/badwords.txt`); the player is back in the lobby. `code` is `challenge` when a challenge link can't be used (expired, your own, or its owner is offline), and `room` when a group game link can't be used (started, closed or full) or the host closed the room you were in. |
| `kicked`  |        | The same session connected from elsewhere; this connection closes. Don't reconnect automatically. |
| `stats`   | `online` | Number of players in running games or waiting in the queue. Sent with `lobby`/`queued` and again (at most once a second) whenever it changes, to players not in a game. |

### `game`

```json
{
  "type": "game",
  "id": "4f1c…",
  "mode": "random",
  "you": 0,
  "players": [
    { "name": "Gergő", "mark": "X", "connected": true, "left": false, "rematch": false },
    { "name": "Bob", "mark": "O", "connected": true, "left": false, "rematch": false }
  ],
  "moves": [[0, 0, 0], [1, 0, 1], [0, 1, 0]],
  "turn": "O",
  "remainingMs": 28412,
  "turnMs": 30000,
  "over": false,
  "winner": "X",
  "reason": "five",
  "line": [[0, 0], [0, 1], [0, 2], [0, 3], [0, 4]]
}
```

- `mode` is `random` (paired from the queue), `friend` (from a challenge
  link) or `room` (a group game). Games against Amőbot and same-device games
  run entirely in the browser and never reach the server.
- `players` lists the seats in play order. Seats play `X` (blue), `O` (red),
  `T` (green triangle) and `S` (amber square); X always moves first. `you`
  is your seat. A player who dropped out of a running game has `out`
  (`timeout` | `resign`); `left` means they left the finished game for good;
  `rematch` means they asked for a rematch.
- `moves` are `[x, y, seat]` in play order.
- `remainingMs` is the time left in the current turn when the message was
  sent; the client counts down locally. 0 when the game is over.
- `winner`, `reason` (`five` | `timeout` | `resign` | `draw`) and `line`
  appear only when `over` is true. `winner` is missing after a draw. `line`
  is only set for `five` and may be longer than 5.

## Rules and timing

- Five or more in a row (horizontal, vertical, diagonal) wins.
- Each turn lasts `TURN_SECONDS` (default 30). Running out of time loses.
- With 3 or 4 players, a player who runs out of time or leaves drops out:
  their marks stay on the board, their turns are skipped, and the last
  player left wins.
- A game that reaches 2000 moves ends in a draw.
- Disconnecting doesn't pause the clock: a player who drops has until the end
  of their turn to reconnect with their token and carry on.
- After a game ends, a player who leaves (back to the lobby or matchmaking,
  or disconnects and doesn't return within 3 s) is gone for good: the others
  see them with `left: true`. A rematch starts once everyone who stayed has
  asked for one, with the seats rotated so the next player moves first. It
  needs at least 2 players (3 in a group game). Your own `rematch` stays set,
  so the client can say "rematch cancelled".
- A player in the queue who disconnects is removed from it (the client rejoins
  automatically on reconnect).
- A group game's room waits for its host through brief disconnects, and
  closes when the host leaves or has been gone for 10 minutes. A guest who
  disconnects is taken out of the room; the client enters again on
  reconnect. The room closes once its game starts.
- A challenge link is single-use. It survives its owner's brief disconnects
  (it can't be accepted while they're away) and lasts until they start any
  game, cancel, or have been gone for 10 minutes.
- The server pings every 25 s to keep idle connections alive through
  proxies and mobile NATs.
