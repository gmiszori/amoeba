// Mirrors server/internal/protocol. See docs/protocol.md.

export type Mark = "X" | "O";
export type Cell = [number, number];

export interface GameState {
  type: "game";
  id: string;
  // "bot" and "local" games never touch the server, see local.ts.
  mode: "random" | "friend" | "bot" | "local";
  you: Mark;
  yourName: string;
  opponentName: string;
  opponentConnected: boolean;
  moves: Cell[]; // in order; even indexes are X
  turn: Mark;
  remainingMs: number;
  turnMs: number;
  over: boolean;
  winner?: Mark;
  reason?: "five" | "timeout" | "resign";
  line?: Cell[];
  rematch: { you: boolean; opponent: boolean };
  opponentLeft: boolean;
}

export type ServerMessage =
  | { type: "welcome"; token: string; name: string }
  | { type: "lobby" }
  | { type: "queued" }
  | { type: "challenge"; code: string } // waiting for a friend to open the link
  | { type: "invite"; code: string; name: string } // reply to peek
  | { type: "kicked" }
  | { type: "stats"; online: number } // players in a game or the queue
  | { type: "error"; code?: "name" | "challenge"; message: string }
  | GameState;

export type ClientMessage =
  | { type: "hello"; token?: string }
  | { type: "join"; name: string }
  | { type: "challenge"; name: string }
  | { type: "peek"; code: string }
  | { type: "accept"; code: string; name: string }
  | { type: "cancel" }
  | { type: "move"; x: number; y: number }
  | { type: "rematch" }
  | { type: "leave" };

export const MAX_NAME_LENGTH = 16;

const NAME_CHARS = /^[\p{L}\p{M}\p{Nd} _.\-]+$/u;

/** Same rules as hub.NormalizeName; returns null when the name is invalid. */
export function normalizeName(raw: string): string | null {
  const name = raw.trim().split(/\s+/).join(" ");
  const length = [...name].length;
  if (length === 0 || length > MAX_NAME_LENGTH || !NAME_CHARS.test(name)) return null;
  return name;
}
