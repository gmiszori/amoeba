import type { Cell, Mark } from "./protocol";

/**
 * Picks moves for Amőbot, the computer opponent. It runs in the browser, so
 * games against it work offline too.
 *
 * It looks one move ahead only. Every empty cell next to an existing mark is
 * scored by the five-cell windows running through it: a window that holds
 * only the bot's marks is worth more the fuller it gets (attack), and so is a
 * window that holds only the opponent's marks (defence, since playing there
 * spoils it). Winning beats blocking a four, which beats everything else.
 * That plays a sensible game, but a player who sets up two threats at once
 * will beat it.
 */

export const BOT_NAME = "Amőbot";

const WIN_LENGTH = 5;

// Scores for a window by how many marks it would hold. ATTACK counts the
// bot's marks including the one being placed; DEFEND counts the opponent's
// marks already there.
const ATTACK = [0, 1, 20, 300, 6_000, 1_000_000];
const DEFEND = [0, 2, 40, 2_500, 200_000];

// Below this best score nothing urgent is going on (no four to make or block,
// no open three to stop), so the bot may pick any move close to the best one
// instead of always the best. It keeps games varied.
const CASUAL = 2_000;

const DIRECTIONS: Cell[] = [
  [1, 0],
  [0, 1],
  [1, 1],
  [1, -1],
];

/** What the bot needs to know about a game. */
export interface Board {
  readonly moves: readonly Cell[];
  at(cell: Cell): Mark | null;
}

/** Returns the cell where `me` should play next. */
export function botMove(board: Board, me: Mark, random: () => number = Math.random): Cell {
  if (board.moves.length === 0) return [0, 0];

  // Only cells touching a mark: every winning or blocking cell does, and
  // replies further out look aimless.
  const options = new Map<string, { cell: Cell; score: number }>();
  let best = 0;
  for (const [mx, my] of board.moves) {
    for (let dx = -1; dx <= 1; dx++) {
      for (let dy = -1; dy <= 1; dy++) {
        const cell: Cell = [mx + dx, my + dy];
        const key = `${cell[0]},${cell[1]}`;
        if (options.has(key) || board.at(cell)) continue;
        const s = score(board, cell, me);
        options.set(key, { cell, score: s });
        best = Math.max(best, s);
      }
    }
  }

  const threshold = best < CASUAL ? best * 0.75 : best;
  const picks = [...options.values()].filter((o) => o.score >= threshold);
  return picks[Math.floor(random() * picks.length)].cell;
}

/** Rates placing `me` at the empty cell [x, y]. */
function score(board: Board, [x, y]: Cell, me: Mark): number {
  let total = 0;
  for (const [dx, dy] of DIRECTIONS) {
    // The five windows of length 5 along this direction that contain the cell.
    for (let k = 0; k < WIN_LENGTH; k++) {
      let mine = 0;
      let theirs = 0;
      for (let i = 0; i < WIN_LENGTH; i++) {
        const mark = board.at([x + (i - k) * dx, y + (i - k) * dy]);
        if (mark === me) mine++;
        else if (mark) theirs++;
      }
      if (theirs === 0) total += ATTACK[mine + 1];
      else if (mine === 0) total += DEFEND[theirs];
    }
  }
  return total;
}
