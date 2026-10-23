package game

import (
	"errors"
	"testing"
)

// play alternates moves starting with X; it fails the test on any error.
func play(t *testing.T, g *Game, moves ...Point) {
	t.Helper()
	for _, p := range moves {
		if err := g.Play(g.Turn(), p); err != nil {
			t.Fatalf("Play(%v): %v", p, err)
		}
	}
}

// interleave builds a move list where X plays xs and O plays os in turn.
func interleave(xs, os []Point) []Point {
	var out []Point
	for i := range xs {
		out = append(out, xs[i])
		if i < len(os) {
			out = append(out, os[i])
		}
	}
	return out
}

func row(x, y, dx, dy, n int) []Point {
	ps := make([]Point, n)
	for i := range ps {
		ps[i] = Point{x + i*dx, y + i*dy}
	}
	return ps
}

func TestWinDirections(t *testing.T) {
	cases := map[string][]Point{
		"horizontal":    row(0, 0, 1, 0, 5),
		"vertical":      row(3, -2, 0, 1, 5),
		"diagonal":      row(-4, -4, 1, 1, 5),
		"anti-diagonal": row(10, 0, 1, -1, 5),
	}
	for name, xs := range cases {
		t.Run(name, func(t *testing.T) {
			g := New()
			play(t, g, interleave(xs, row(100, 100, 0, 1, 4))...)
			if !g.Over() || g.Winner() != X || g.Reason() != Five {
				t.Fatalf("over=%v winner=%v reason=%v, want X to win by five", g.Over(), g.Winner(), g.Reason())
			}
			if len(g.Line()) != 5 {
				t.Fatalf("line length %d, want 5", len(g.Line()))
			}
		})
	}
}

func TestFourDoesNotWin(t *testing.T) {
	g := New()
	play(t, g, interleave(row(0, 0, 1, 0, 4), row(0, 5, 1, 0, 4))...)
	if g.Over() {
		t.Fatal("four in a row ended the game")
	}
}

func TestOverlineWins(t *testing.T) {
	// X builds XXX_XX, then fills the gap to make six.
	xs := []Point{{0, 0}, {1, 0}, {2, 0}, {4, 0}, {5, 0}, {3, 0}}
	os := row(0, 9, 3, 0, 5) // spread out so O never wins
	g := New()
	play(t, g, interleave(xs, os)...)
	if g.Winner() != X {
		t.Fatalf("winner %v, want X", g.Winner())
	}
	if len(g.Line()) != 6 {
		t.Fatalf("line length %d, want 6", len(g.Line()))
	}
}

func TestOWins(t *testing.T) {
	g := New()
	play(t, g, interleave(row(0, 9, 3, 0, 5), row(0, 0, 0, 1, 5))...)
	if g.Winner() != O {
		t.Fatalf("winner %v, want O", g.Winner())
	}
}

func TestRuleErrors(t *testing.T) {
	g := New()
	if err := g.Play(O, Point{0, 0}); !errors.Is(err, ErrNotYourTurn) {
		t.Fatalf("O moving first: got %v", err)
	}
	play(t, g, Point{0, 0})
	if err := g.Play(O, Point{0, 0}); !errors.Is(err, ErrOccupied) {
		t.Fatalf("occupied cell: got %v", err)
	}
	g.Forfeit(O, Timeout)
	if g.Winner() != X || g.Reason() != Timeout {
		t.Fatalf("after forfeit: winner=%v reason=%v", g.Winner(), g.Reason())
	}
	if err := g.Play(O, Point{1, 1}); !errors.Is(err, ErrGameOver) {
		t.Fatalf("move after game over: got %v", err)
	}
	g.Forfeit(X, Resign) // no effect once over
	if g.Winner() != X {
		t.Fatal("second forfeit changed the winner")
	}
}

func TestMoveLimit(t *testing.T) {
	g := New()
	for i := range MaxMoves {
		// Spread out so nobody ever gets five in a row.
		p := Point{(i / 2) * 2, (i % 2) * 50}
		if err := g.Play(g.Turn(), p); err != nil {
			t.Fatalf("move %d: %v", i, err)
		}
	}
	if err := g.Play(g.Turn(), Point{-1, -1}); !errors.Is(err, ErrBoardFull) {
		t.Fatalf("move past the limit: got %v", err)
	}
}
