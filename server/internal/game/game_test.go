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
			g := New(2)
			// O plays a column two cells left of X's first mark.
			play(t, g, interleave(xs, row(xs[0].X-2, xs[0].Y, 0, 1, 4))...)
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
	g := New(2)
	play(t, g, interleave(row(0, 0, 1, 0, 4), row(0, 5, 1, 0, 4))...)
	if g.Over() {
		t.Fatal("four in a row ended the game")
	}
}

func TestOverlineWins(t *testing.T) {
	// X builds XXX_XX, then fills the gap to make six.
	xs := []Point{{0, 0}, {1, 0}, {2, 0}, {4, 0}, {5, 0}, {3, 0}}
	os := row(0, 3, 3, 0, 5) // spread out so O never wins
	g := New(2)
	play(t, g, interleave(xs, os)...)
	if g.Winner() != X {
		t.Fatalf("winner %v, want X", g.Winner())
	}
	if len(g.Line()) != 6 {
		t.Fatalf("line length %d, want 6", len(g.Line()))
	}
}

func TestOWins(t *testing.T) {
	g := New(2)
	play(t, g, interleave(row(3, 0, 3, 0, 5), row(0, 0, 0, 1, 5))...)
	if g.Winner() != O {
		t.Fatalf("winner %v, want O", g.Winner())
	}
}

func TestRuleErrors(t *testing.T) {
	g := New(2)
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

func TestMoveLimitDraws(t *testing.T) {
	g := New(2)
	for i := range MaxMoves {
		// Every other cell of two rows apart, so nobody ever gets five in a row.
		p := Point{(i / 2) * 2, (i % 2) * 5}
		if err := g.Play(g.Turn(), p); err != nil {
			t.Fatalf("move %d: %v", i, err)
		}
	}
	if !g.Over() || g.Winner() != None || g.Reason() != Draw {
		t.Fatalf("at the limit: over=%v winner=%v reason=%v, want a draw", g.Over(), g.Winner(), g.Reason())
	}
	if err := g.Play(g.Turn(), Point{-1, -1}); !errors.Is(err, ErrGameOver) {
		t.Fatalf("move past the limit: got %v", err)
	}
}

func TestMarksStayNearEachOther(t *testing.T) {
	g := New(2)
	play(t, g, Point{100, 100}) // the first mark goes anywhere
	if err := g.Play(O, Point{106, 100}); !errors.Is(err, ErrTooFar) {
		t.Fatalf("six cells away: got %v", err)
	}
	if err := g.Play(O, Point{95, 95}); err != nil {
		t.Fatalf("five cells away diagonally: %v", err)
	}
	if err := g.Play(X, Point{100, 89}); !errors.Is(err, ErrTooFar) {
		t.Fatalf("six cells from the nearest mark: got %v", err)
	}
}

func TestFourPlayersTakeTurns(t *testing.T) {
	g := New(4)
	for i, want := range []Mark{X, O, T, S, X} {
		if g.Turn() != want {
			t.Fatalf("move %d: turn %v, want %v", i, g.Turn(), want)
		}
		play(t, g, Point{i * 3, 0})
	}
	for i, m := range g.Moves() {
		if want := SeatMark(i % 4); m.Mark != want {
			t.Fatalf("move %d recorded as %v, want %v", i, m.Mark, want)
		}
	}
}

func TestThirdPlayerWins(t *testing.T) {
	g := New(3)
	for i := range 5 {
		play(t, g, Point{i * 2, 2}, Point{i * 2, 4}, Point{i, 0}) // X and O spread out
		if g.Over() {
			if i != 4 || g.Winner() != T || g.Reason() != Five {
				t.Fatalf("after round %d: winner=%v reason=%v, want T by five in round 4", i, g.Winner(), g.Reason())
			}
			return
		}
	}
	t.Fatal("nobody won")
}

func TestForfeitSkipsPlayer(t *testing.T) {
	g := New(3)
	play(t, g, Point{0, 0}) // X; O to move
	g.Forfeit(O, Timeout)
	if g.Over() || g.Turn() != T || g.Out(O) != Timeout {
		t.Fatalf("after O timed out: over=%v turn=%v out=%q", g.Over(), g.Turn(), g.Out(O))
	}
	play(t, g, Point{1, 0}) // T
	if g.Turn() != X {
		t.Fatalf("after T: turn %v, want X (O is out)", g.Turn())
	}
	g.Forfeit(T, Resign) // not T's turn; X keeps it
	if !g.Over() || g.Winner() != X || g.Reason() != Resign {
		t.Fatalf("last player standing: over=%v winner=%v reason=%v", g.Over(), g.Winner(), g.Reason())
	}
}

func TestForfeitOffTurnKeepsTurn(t *testing.T) {
	g := New(4)
	g.Forfeit(T, Resign)
	if g.Over() || g.Turn() != X {
		t.Fatalf("over=%v turn=%v, want X still to move", g.Over(), g.Turn())
	}
	play(t, g, Point{0, 0}, Point{1, 0}) // X, O
	if g.Turn() != S {
		t.Fatalf("turn %v, want S (T is out)", g.Turn())
	}
}
