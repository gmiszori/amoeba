import { alternating, BoardView } from "./board";
import type { Cell, Mark } from "./protocol";

// A finished game for the home screen's background, laid out so the marks
// sit to the left and right of the menu. Portrait screens don't show it (see
// global.css): the menu leaves no room. Listed in play order, X first; X's
// last move wins along `line`.
interface Sample {
  moves: Cell[];
  line: Cell[];
  winner: Mark;
}

const SAMPLE: Sample = {
  moves: [
    [6, -4], [5, -3],
    [-8, 2], [-6, 2],
    [7, -3], [8, -3],
    [-7, 2], [-8, 3],
    [8, -2], [6, -2],
    [-9, 4], [-7, 3],
    [-5, -5], [-4, -5],
    [-3, 6], [-2, 6],
    [3, 5], [4, 5],
    [-10, -2], [-10, -1],
    [9, -1], [11, 1],
    [10, 0],
  ],
  line: [[6, -4], [7, -3], [8, -2], [9, -1], [10, 0]],
  winner: "X",
};

/** Draws the decorative board behind the home screen. */
export function drawBackdrop(canvas: HTMLCanvasElement): void {
  const view = new BoardView(canvas);
  view.setModel(SAMPLE.moves, alternating(SAMPLE.moves.length), SAMPLE.line, SAMPLE.winner);
  // Keep the game framed as the window changes size. BoardView measures the
  // canvas in its own observer, so wait for that to have run.
  new ResizeObserver(() => requestAnimationFrame(() => view.recenter())).observe(canvas);
}
