import type { Cell, GameState, Mark } from "./protocol";

const WIN_LENGTH = 5;
const DIRECTIONS: Cell[] = [
  [1, 0],
  [0, 1],
  [1, 1],
  [1, -1],
];

/**
 * A game for two players sharing one device. It never touches the server, so
 * the rules are repeated here: same as server/internal/game (X first, five or
 * more in a row wins), but without a turn clock.
 */
export class LocalGame {
  readonly id = `local-${Date.now()}`;
  private readonly cells = new Map<string, Mark>();
  private readonly moves: Cell[] = [];
  private turn: Mark = "X";
  private winner: Mark | null = null;
  private line: Cell[] | null = null;

  /** Places the current player's mark; returns false if the cell is taken or the game is over. */
  play(cell: Cell): boolean {
    if (this.winner || this.cells.has(key(cell))) return false;
    this.cells.set(key(cell), this.turn);
    this.moves.push(cell);
    const line = this.lineThrough(cell);
    if (line) {
      this.winner = this.turn;
      this.line = line;
    } else {
      this.turn = this.turn === "X" ? "O" : "X";
    }
    return true;
  }

  /**
   * The game as the online screens expect it. "you" is always the player to
   * move, since both players are at this device.
   */
  state(): GameState {
    return {
      type: "game",
      id: this.id,
      you: this.turn,
      yourName: "",
      opponentName: "",
      opponentConnected: true,
      moves: this.moves,
      turn: this.turn,
      remainingMs: 0,
      turnMs: 0,
      over: this.winner !== null,
      winner: this.winner ?? undefined,
      reason: this.winner ? "five" : undefined,
      line: this.line ?? undefined,
      rematch: { you: false, opponent: false },
      opponentLeft: false,
    };
  }

  private lineThrough([x, y]: Cell): Cell[] | null {
    const mark = this.cells.get(key([x, y]));
    for (const [dx, dy] of DIRECTIONS) {
      let [sx, sy] = [x, y];
      while (this.cells.get(key([sx - dx, sy - dy])) === mark) [sx, sy] = [sx - dx, sy - dy];
      const line: Cell[] = [];
      for (let [cx, cy] = [sx, sy]; this.cells.get(key([cx, cy])) === mark; cx += dx, cy += dy) {
        line.push([cx, cy]);
      }
      if (line.length >= WIN_LENGTH) return line;
    }
    return null;
  }
}

const key = ([x, y]: Cell) => `${x},${y}`;
