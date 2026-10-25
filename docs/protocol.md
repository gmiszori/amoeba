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
| `room`    | `name`          | Open a room for playing with friends, as its host (answered with `room`). Asking again while hosting returns the same room. |
| `peekRoom` | `code`         | Look up a room from a `/r/<code>` link (answered with `roomInvite`). |
| `enter`   | `code`, `name`  | Take a seat in a room. During a game you wait in the room and play in the next one. |
| `start`   |                 | Host only: start a game with the players in the room (2–4). |
| `toRoom`  |                 | After a game with friends: go back to the room for the next one. |
| `cancel`  |                 | Leave the matchmaking queue or a room. When the host leaves, the room closes. |
| `move`    | `x`, `y`        | Place your mark at cell (x, y). Integers, \|x\|,\|y\| ≤ 1,000,000, and within 5 cells of an existing mark. |
| `rematch` |                 | Ask for (or accept) a rematch after a two-player game ends. Colours swap. |
| `leave`   |                 | Back to the lobby. Resigns a running game (with 3 or more players, the others play on). |

## Server → client

| type      | fields | meaning |
|-----------|--------|---------|
| `welcome` | `token`, `name` | Reply to `hello`. Store the token (local storage). |
| `lobby`   |        | Not queued, not in a game. |
| `queued`  |        | Waiting for an opponent. |
| `invite`  | `code`, `name` | Reply to `peek`: `name` is challenging you. |
| `room`    | `code`, `host`, `players`, `you`, `max`, `min`, `playing` | You're waiting in a room. `players` is `[{name, connected, host}]`, those waiting for the next game in joining order (the host first when they're in the room); `you` indexes it. `playing` means a game is in progress, without you. Sent whenever the room changes and on reconnect. |
| `roomInvite` | `code`, `host`, `players`, `max`, `playing` | Reply to `peekRoom`: the host's name, the names of those waiting in the room, and whether a game is in progress. |
| `game`    | see below | Full state of your current match. Sent whenever it changes. |
| `error`   | `code?`, `message` | A request was rejected. `code` is `name` when the username was invalid or blocked (see `server/internal/hub/badwords.txt`); the player is back in the lobby. `code` is `room` when a room link can't be used (closed or full) or the room you were in was closed. |
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

- `mode` is `random` (paired from the queue) or `room` (with friends). Games against Amőbot and same-device games
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
- `room` is set after a game with friends while you can go back to its
  room (`toRoom`); it disappears once the host has left or the next game
  has started.

## Rules and timing

- Five or more in a row (horizontal, vertical, diagonal) wins.
- Every mark after the first must be within 5 cells (in any direction,
  diagonals included) of an existing mark, so play stays in one place.
- Each turn lasts `TURN_SECONDS` (default 30). Running out of time loses.
- With 3 or 4 players, a player who runs out of time or leaves drops out:
  their marks stay on the board, their turns are skipped, and the last
  player left wins.
- A game that reaches 2000 moves ends in a draw.
- Disconnecting doesn't pause the clock: a player who drops has until the end
  of their turn to reconnect with their token and carry on.
- After a game ends, a player who leaves (back to the lobby or matchmaking,
  or disconnects and doesn't return within 3 s) is gone for good: the others
  see them with `left: true`. In a two-player game a rematch starts once
  both have asked for one, with colours swapped. Your own `rematch` stays
  set, so the client can say "rematch cancelled".
- A player in the queue who disconnects is removed from it (the client rejoins
  automatically on reconnect).
- A room holds 4 players: those waiting in it, plus those still in its
  game or looking at its result. When it's full, its link says so. Only the
  host starts a game, with 2 to 4 players; seats are drawn at random.
- During a game newcomers can take a free seat and wait in the room. When
  the game ends, the host is back in the room and the others return with
  `toRoom`. Seats of players who leave instead are free again.
- A guest who disconnects keeps their seat for 10 s (shown offline); after
  that they're out, and the client enters again on reconnect. A room waits
  1 minute for a host who disconnects, then closes. It also closes when the
  host leaves it, or leaves a game started from it.
- The server pings every 25 s to keep idle connections alive through
  proxies and mobile NATs.
