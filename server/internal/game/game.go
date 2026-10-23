// Package game implements the rules of amőba: two players alternate placing
// marks on an unbounded grid, and the first to get five or more in a row
// (horizontally, vertically or diagonally) wins.
package game

import "errors"

// WinLength is the number of marks in a row needed to win. Longer runs win too.
const WinLength = 5

// MaxMoves caps the size of a game so it can't grow without bound. Real games
// end long before this.
const MaxMoves = 2000

// Mark is the content of a cell.
type Mark uint8

const (
	None Mark = iota
	X         // blue, always moves first
	O         // red
)

func (m Mark) String() string {
	switch m {
	case X:
		return "X"
	case O:
		return "O"
	}
	return ""
}

// Other returns the opposing mark.
func (m Mark) Other() Mark {
	switch m {
	case X:
		return O
	case O:
		return X
	}
	return None
}

// Point is a cell coordinate. The grid is unbounded in every direction.
type Point struct{ X, Y int }

// Reason says why a game ended.
type Reason string

const (
	Five    Reason = "five"
	Timeout Reason = "timeout"
	Resign  Reason = "resign"
)

var (
	ErrGameOver    = errors.New("the game is over")
	ErrNotYourTurn = errors.New("it is not your turn")
	ErrOccupied    = errors.New("that cell is already taken")
	ErrBoardFull   = errors.New("the move limit for this game has been reached")
)

// Game is a single match. The board is stored sparsely, so its size only
// depends on the number of moves played. A Game is not safe for concurrent use.
type Game struct {
	cells  map[Point]Mark
	moves  []Point
	turn   Mark
	winner Mark
	reason Reason
	line   []Point
}

// New returns an empty game with X to move.
func New() *Game {
	return &Game{cells: make(map[Point]Mark), turn: X}
}

func (g *Game) Turn() Mark      { return g.turn }
func (g *Game) Over() bool      { return g.winner != None }
func (g *Game) Winner() Mark    { return g.winner }
func (g *Game) Reason() Reason  { return g.reason }
func (g *Game) At(p Point) Mark { return g.cells[p] }

// Moves returns the moves in the order they were played. Even indexes are X,
// odd indexes are O. The returned slice must not be modified.
func (g *Game) Moves() []Point { return g.moves }

// Line returns the winning run of cells when the game was won by five in a row.
func (g *Game) Line() []Point { return g.line }

// Play places m at p.
func (g *Game) Play(m Mark, p Point) error {
	if g.Over() {
		return ErrGameOver
	}
	if m != g.turn {
		return ErrNotYourTurn
	}
	if g.cells[p] != None {
		return ErrOccupied
	}
	if len(g.moves) >= MaxMoves {
		return ErrBoardFull
	}
	g.cells[p] = m
	g.moves = append(g.moves, p)
	if line := g.lineThrough(p); line != nil {
		g.winner, g.reason, g.line = m, Five, line
		return nil
	}
	g.turn = m.Other()
	return nil
}

// Forfeit ends the game with loser losing for the given reason.
func (g *Game) Forfeit(loser Mark, reason Reason) {
	if g.Over() {
		return
	}
	g.winner, g.reason = loser.Other(), reason
}

var directions = [...]Point{{1, 0}, {0, 1}, {1, 1}, {1, -1}}

// lineThrough returns a run of at least WinLength equal
// marks passing through p, or nil. Only lines through the newest mark can be
// new, so this is all the win check ever needs to look at.
func (g *Game) lineThrough(p Point) []Point {
	m := g.cells[p]
	for _, d := range directions {
		start := p
		for g.cells[Point{start.X - d.X, start.Y - d.Y}] == m {
			start = Point{start.X - d.X, start.Y - d.Y}
		}
		var line []Point
		for q := start; g.cells[q] == m; q = (Point{q.X + d.X, q.Y + d.Y}) {
			line = append(line, q)
		}
		if len(line) >= WinLength {
			return line
		}
	}
	return nil
}
