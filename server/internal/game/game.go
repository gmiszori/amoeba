// Package game implements the rules of amőba: two to four players take turns
// placing marks on an unbounded grid, and the first to get five or more in a
// row (horizontally, vertically or diagonally) wins. A player who resigns or
// runs out of time drops out: their marks stay on the board, their turns are
// skipped, and the last player left wins.
package game

import (
	"errors"
	"fmt"
)

// WinLength is the number of marks in a row needed to win. Longer runs win too.
const WinLength = 5

// MaxMoves caps the size of a game so it can't grow without bound. A game
// that reaches it ends in a draw; real games end long before this.
const MaxMoves = 2000

// MaxPlayers is the largest number of players in one game.
const MaxPlayers = 4

// Mark is the content of a cell. Players are seated in mark order, and X
// always moves first.
type Mark uint8

const (
	None Mark = iota
	X         // blue
	O         // red
	T         // green triangle
	S         // amber square
)

func (m Mark) String() string {
	switch m {
	case X:
		return "X"
	case O:
		return "O"
	case T:
		return "T"
	case S:
		return "S"
	}
	return ""
}

// SeatMark returns the mark played from seat (0-based).
func SeatMark(seat int) Mark { return Mark(seat + 1) }

// Point is a cell coordinate. The grid is unbounded in every direction.
type Point struct{ X, Y int }

// Move is a mark placed on the board.
type Move struct {
	Point
	Mark Mark
}

// Reason says why a game ended, or why a player dropped out of it.
type Reason string

const (
	Five    Reason = "five"
	Timeout Reason = "timeout"
	Resign  Reason = "resign"
	Draw    Reason = "draw" // the move limit was reached
)

var (
	ErrGameOver    = errors.New("the game is over")
	ErrNotYourTurn = errors.New("it is not your turn")
	ErrOccupied    = errors.New("that cell is already taken")
)

// Game is a single match. The board is stored sparsely, so its size only
// depends on the number of moves played. A Game is not safe for concurrent use.
type Game struct {
	players int
	cells   map[Point]Mark
	moves   []Move
	turn    Mark
	out     [MaxPlayers + 1]Reason // by mark; empty while still playing
	over    bool
	winner  Mark
	reason  Reason
	line    []Point
}

// New returns an empty game for the given number of players with X to move.
func New(players int) *Game {
	if players < 2 || players > MaxPlayers {
		panic(fmt.Sprintf("game: %d players", players))
	}
	return &Game{players: players, cells: make(map[Point]Mark), turn: X}
}

func (g *Game) Players() int    { return g.players }
func (g *Game) Turn() Mark      { return g.turn }
func (g *Game) Over() bool      { return g.over }
func (g *Game) Reason() Reason  { return g.reason }
func (g *Game) At(p Point) Mark { return g.cells[p] }

// Winner returns the winning mark, or None while the game runs or after a draw.
func (g *Game) Winner() Mark { return g.winner }

// Out returns why m dropped out of the game, or "" if it hasn't.
func (g *Game) Out(m Mark) Reason { return g.out[m] }

// Moves returns the moves in the order they were played. The returned slice
// must not be modified.
func (g *Game) Moves() []Move { return g.moves }

// Line returns the winning run of cells when the game was won by five in a row.
func (g *Game) Line() []Point { return g.line }

// Play places m at p.
func (g *Game) Play(m Mark, p Point) error {
	if g.over {
		return ErrGameOver
	}
	if m != g.turn {
		return ErrNotYourTurn
	}
	if g.cells[p] != None {
		return ErrOccupied
	}
	g.cells[p] = m
	g.moves = append(g.moves, Move{p, m})
	switch line := g.lineThrough(p); {
	case line != nil:
		g.finish(m, Five, line)
	case len(g.moves) >= MaxMoves:
		g.finish(None, Draw, nil)
	default:
		g.turn = g.next(m)
	}
	return nil
}

// Forfeit takes m out of the game for the given reason. When only one player
// is left, they win; otherwise play goes on without m.
func (g *Game) Forfeit(m Mark, reason Reason) {
	if g.over || g.out[m] != "" {
		return
	}
	g.out[m] = reason
	if next := g.next(m); g.next(next) == next {
		g.finish(next, reason, nil)
	} else if g.turn == m {
		g.turn = next
	}
}

// next returns the first player after m who is still in the game.
func (g *Game) next(m Mark) Mark {
	for i := 1; i < g.players; i++ {
		if c := Mark((int(m)-1+i)%g.players + 1); g.out[c] == "" {
			return c
		}
	}
	return m
}

func (g *Game) finish(winner Mark, reason Reason, line []Point) {
	g.over, g.winner, g.reason, g.line = true, winner, reason, line
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
