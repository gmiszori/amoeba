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

const TALL: Sample = {
  moves: [
    [1, -13], [0, -13],
    [-3, 9], [-3, 10],
    [2, -12], [2, -13],
    [-2, 10], [-1, 9],
    [-2, -11], [3, -11],
    [-1, 11], [0, 11],
    [3, -14], [-4, -12],
    [3, 12], [2, 11],
    [-4, 13], [-3, 14],
    [0, 12], [4, 9],
    [1, 13],
  ],
  line: [[-3, 9], [-2, 10], [-1, 11], [0, 12], [1, 13]],
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
