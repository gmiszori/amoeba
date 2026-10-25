import { BOT_NAME, type Board } from "./bot";
import type { Cell, GameState, Mark } from "./protocol";

const WIN_LENGTH = 5;
const DIRECTIONS: Cell[] = [
  [1, 0],
  [0, 1],
  [1, 1],
  [1, -1],
];

/**
 * A game that never touches the server: either two players sharing one
 * device, or one player against Amőbot. The rules are repeated here: same as
 * server/internal/game (X first, five or more in a row wins), but without a
 * turn clock.
 */
export class LocalGame implements Board {
  readonly id = `local-${Date.now()}`;
  readonly moves: Cell[] = [];
  private readonly cells = new Map<string, Mark>();
  private turn: Mark = "X";
  private winner: Mark | null = null;
  private line: Cell[] | null = null;

  /**
   * @param bot the mark Amőbot plays, or null for two players on this device
   * @param playerName shown for the human player in a game against Amőbot
   */
  constructor(
    readonly bot: Mark | null = null,
    private readonly playerName = "",
  ) {}

  get over(): boolean {
    return this.winner !== null;
  }

  get toMove(): Mark {
    return this.turn;
  }

  at(cell: Cell): Mark | null {
    return this.cells.get(key(cell)) ?? null;
  }

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
   * The game as the online screens expect it. On a shared device "you" is
   * always the player to move, since both players are at this device.
   */
  state(): GameState {
    const vsBot = this.bot !== null;
    const names = vsBot ? [this.playerName, BOT_NAME] : ["", ""];
    if (this.bot === "X") names.reverse();
    return {
      type: "game",
      id: this.id,
      mode: vsBot ? "bot" : "local",
      you: vsBot ? (this.bot === "X" ? 1 : 0) : this.turn === "X" ? 0 : 1,
      players: (["X", "O"] as const).map((mark, i) => ({
        name: names[i],
        mark,
        connected: true,
        left: false,
        rematch: false,
      })),
      moves: this.moves.map(([x, y], i) => [x, y, i % 2]),
      turn: this.turn,
      remainingMs: 0,
      turnMs: 0,
      over: this.winner !== null,
      winner: this.winner ?? undefined,
      reason: this.winner ? "five" : undefined,
      line: this.line ?? undefined,
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
