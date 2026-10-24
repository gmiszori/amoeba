// Run with `npm test` (Node strips the TypeScript types itself).
import assert from "node:assert/strict";
import { test } from "node:test";

import { botMove, type Board } from "../src/scripts/bot.ts";
import type { Cell, Mark } from "../src/scripts/protocol.ts";

/** A board from alternating X, O moves. */
function board(...moves: Cell[]): Board & { toMove: Mark } {
  const cells = new Map<string, Mark>(moves.map(([x, y], i) => [`${x},${y}`, i % 2 === 0 ? "X" : "O"]));
  return {
    moves,
    at: ([x, y]) => cells.get(`${x},${y}`) ?? null,
    toMove: moves.length % 2 === 0 ? "X" : "O",
  };
}

/** Checks the bot's choice over many random seeds, since it picks randomly between equally good moves. */
function expectOneOf(b: ReturnType<typeof board>, ...want: Cell[]): void {
  for (let i = 0; i < 50; i++) {
    const got = botMove(b, b.toMove, () => i / 50);
    assert.ok(
      want.some(([x, y]) => got[0] === x && got[1] === y),
      `run ${i}: bot played ${got}, want one of ${JSON.stringify(want)}`,
    );
  }
}

test("opens in the middle", () => {
  expectOneOf(board(), [0, 0]);
});

test("completes five rather than blocking", () => {
  // Both have four; O to move must win.
  const b = board([0, 0], [0, 5], [1, 0], [1, 5], [2, 0], [2, 5], [3, 0], [3, 5], [9, 9]);
  expectOneOf(b, [4, 5], [-1, 5]);
});

test("blocks a four", () => {
  const b = board([0, 0], [-1, 0], [1, 0], [5, 5], [2, 0], [6, 7], [3, 0]);
  expectOneOf(b, [4, 0]);
});

test("blocks an open three", () => {
  const b = board([0, 0], [5, -3], [1, 1], [-4, 6], [2, 2]);
  expectOneOf(b, [-1, -1], [3, 3]);
});

test("plays next to the action", () => {
  const b = board([10, 10]);
  for (let i = 0; i < 20; i++) {
    const [x, y] = botMove(b, "O", () => i / 20);
    assert.ok(Math.abs(x - 10) <= 1 && Math.abs(y - 10) <= 1, `reply ${x},${y} is not next to the first mark`);
  }
});
