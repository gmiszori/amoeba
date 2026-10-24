import { BoardView } from "./board";
import type { Cell, Mark } from "./protocol";

// Finished games for the home screen's background, laid out so the marks sit
// around the menu rather than behind it: to its left and right on wide
// screens, above and below it on tall ones. Listed in play order, X first;
// X's last move wins along `line`.
interface Sample {
  moves: Cell[];
  line: Cell[];
  winner: Mark;
}

const WIDE: Sample = {
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

// Kept to rows 11–14 above and below, so that even on short phones the marks
// clear the menu in the middle.
const TALL: Sample = {
  moves: [
    [-2, 12], [-1, 11],
    [1, -12], [0, -12],
    [-1, 12], [0, 13],
    [2, -13], [2, -12],
    [0, 12], [-3, 12],
    [-2, -11], [3, -14],
    [1, 12], [1, 13],
    [-3, -13], [-1, -14],
    [3, 11], [-2, 14],
    [2, 12],
  ],
  line: [[-2, 12], [-1, 12], [0, 12], [1, 12], [2, 12]],
  winner: "X",
};

/** Draws the decorative board behind the home screen. */
export function drawBackdrop(canvas: HTMLCanvasElement): void {
  const view = new BoardView(canvas);
  let shown: Sample | null = null;
  // Keep the game framed as the window changes size. BoardView measures the
  // canvas in its own observer, so wait for that to have run.
  new ResizeObserver(() =>
    requestAnimationFrame(() => {
      const sample = canvas.clientHeight > canvas.clientWidth ? TALL : WIDE;
      if (sample !== shown) {
        shown = sample;
        view.setModel(sample.moves, sample.line, sample.winner);
      }
      view.recenter();
    }),
  ).observe(canvas);
}
