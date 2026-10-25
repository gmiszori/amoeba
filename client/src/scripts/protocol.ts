// Mirrors server/internal/protocol. See docs/protocol.md.

/** Seats play X, O, T (triangle) and S (square) in order; X moves first. */
export type Mark = "X" | "O" | "T" | "S";
export const MARKS: readonly Mark[] = ["X", "O", "T", "S"];
export type Cell = [number, number];
/** A placed mark: [x, y, seat], where seat indexes GameState.players. */
export type Move = [number, number, number];

export interface Player {
  name: string;
  mark: Mark;
  connected: boolean;
  out?: "timeout" | "resign"; // dropped out of a running game
  left: boolean; // left the finished game for good; no rematch with them
  rematch: boolean; // asked for a rematch
}

export interface GameState {
  type: "game";
  id: string;
  // "bot" and "local" games never touch the server, see local.ts.
  mode: "random" | "friend" | "room" | "bot" | "local";
  you: number; // your seat: index into players
  players: Player[];
  moves: Move[]; // in play order
  turn: Mark;
  remainingMs: number;
  turnMs: number;
  over: boolean;
  winner?: Mark; // missing after a draw
  reason?: "five" | "timeout" | "resign" | "draw";
  line?: Cell[];
  room?: string; // group games: the room's code while it's open to go back to
}

/** The waiting room of a multiplayer game. */
export interface RoomState {
  type: "room";
  code: string;
  players: { name: string; connected: boolean }[]; // in joining order; the first is the host
  you: number;
  max: number;
  min: number; // players needed before the host can start
}

export type ServerMessage =
  | { type: "welcome"; token: string; name: string }
  | { type: "lobby" }
  | { type: "queued" }
  | { type: "challenge"; code: string } // waiting for a friend to open the link
  | { type: "invite"; code: string; name: string } // reply to peek
  | { type: "roomInvite"; code: string; players: string[]; max: number } // reply to peekRoom
  | { type: "kicked" }
  | { type: "stats"; online: number } // players in a game or the queue
  | { type: "error"; code?: "name" | "challenge" | "room"; message: string }
  | RoomState
  | GameState;

export type ClientMessage =
  | { type: "hello"; token?: string }
  | { type: "join"; name: string }
  | { type: "challenge"; name: string }
  | { type: "peek"; code: string }
  | { type: "accept"; code: string; name: string }
  | { type: "room"; name: string }
  | { type: "peekRoom"; code: string }
  | { type: "enter"; code: string; name: string }
  | { type: "start" }
  | { type: "toRoom" }
  | { type: "cancel" }
  | { type: "move"; x: number; y: number }
  | { type: "rematch" }
  | { type: "leave" };

export const MAX_NAME_LENGTH = 16;

/** How far a new mark may be from the nearest one; same as game.MaxReach. */
export const MAX_REACH = 5;

/** Whether cell is within MAX_REACH cells of a mark, by the given lookup. */
export function inReach([x, y]: Cell, taken: (cell: Cell) => boolean): boolean {
  for (let dx = -MAX_REACH; dx <= MAX_REACH; dx++) {
    for (let dy = -MAX_REACH; dy <= MAX_REACH; dy++) {
      if (taken([x + dx, y + dy])) return true;
    }
  }
  return false;
}

const NAME_CHARS = /^[\p{L}\p{M}\p{Nd} _.\-]+$/u;

/** Same rules as hub.NormalizeName; returns null when the name is invalid. */
export function normalizeName(raw: string): string | null {
  const name = raw.trim().split(/\s+/).join(" ");
  const length = [...name].length;
  if (length === 0 || length > MAX_NAME_LENGTH || !NAME_CHARS.test(name)) return null;
  return name;
}
