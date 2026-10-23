// Package hub owns all live state: player sessions, the matchmaking queue and
// running matches. Everything is kept in memory and guarded by a single mutex,
// which is plenty for a game where each action is a few map operations.
package hub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	mrand "math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/gmiszori/amoeba/server/internal/game"
	"github.com/gmiszori/amoeba/server/internal/protocol"
)

const (
	// maxCoord keeps coordinates in a sane range; nobody pans a million cells.
	maxCoord = 1_000_000
	// sessionTTL is how long a disconnected session outside a running game is
	// kept so a returning player can pick up where they left off.
	sessionTTL    = 10 * time.Minute
	sweepInterval = time.Minute
)

type Config struct {
	TurnTime time.Duration
	// OriginPatterns lists extra allowed WebSocket origins (host patterns).
	// Same-host origins are always allowed.
	OriginPatterns []string
}

type Hub struct {
	cfg Config

	mu       sync.Mutex
	sessions map[string]*session // by token
	queue    []*session
}

// session is a player identity that survives reconnects. The client keeps its
// token in local storage and presents it in hello.
type session struct {
	token    string
	name     string
	client   *client // nil while disconnected
	match    *match  // current or just-finished match; nil in the lobby
	queued   bool
	lastSeen time.Time
}

type match struct {
	id       string
	players  [2]*session // seat 0 plays X, seat 1 plays O
	game     *game.Game
	deadline time.Time
	timer    *time.Timer
	timerGen int // invalidates timers that fire after being replaced
	rematch  [2]bool
	left     [2]bool
}

func (m *match) seat(s *session) int {
	if m.players[0] == s {
		return 0
	}
	return 1
}

func seatMark(seat int) game.Mark {
	if seat == 0 {
		return game.X
	}
	return game.O
}

func New(cfg Config) *Hub {
	return &Hub{cfg: cfg, sessions: make(map[string]*session)}
}

// Run periodically forgets sessions that have been gone for a while. It
// returns when ctx is done.
func (h *Hub) Run(ctx context.Context) {
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			h.sweep(now)
		}
	}
}

func (h *Hub) sweep(now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for token, s := range h.sessions {
		if s.client != nil || now.Sub(s.lastSeen) < sessionTTL {
			continue
		}
		if m := s.match; m != nil {
			if !m.game.Over() {
				continue // the turn timer will finish it
			}
			h.detach(s)
		}
		delete(h.sessions, token)
	}
}

func (h *Hub) handle(c *client, msg protocol.Inbound) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if msg.Type == "hello" {
		h.hello(c, msg.Token)
		return
	}
	s := c.session
	if s == nil {
		c.sendError("say hello first")
		return
	}
	s.lastSeen = time.Now()
	switch msg.Type {
	case "join":
		h.join(s, msg.Name)
	case "cancel":
		h.removeFromQueue(s)
		h.sendStatus(s)
	case "move":
		h.move(s, msg.X, msg.Y)
	case "rematch":
		h.requestRematch(s)
	case "leave":
		h.leave(s)
	default:
		c.sendError("unknown message type")
	}
}

func (h *Hub) hello(c *client, token string) {
	if c.session != nil {
		c.sendError("already said hello")
		return
	}
	s := h.sessions[token]
	if s == nil {
		s = &session{token: newID()}
		h.sessions[s.token] = s
	}
	if old := s.client; old != nil {
		// The same player opened the game somewhere else; the newest wins.
		old.session = nil
		old.send(protocol.Simple{Type: "kicked"})
		old.closeSoon()
	}
	s.client = c
	s.lastSeen = time.Now()
	c.session = s
	c.send(protocol.Welcome{Type: "welcome", Token: s.token, Name: s.name})
	if s.match != nil {
		h.broadcast(s.match) // also tells the opponent we're back
		return
	}
	h.sendStatus(s)
}

func (h *Hub) disconnect(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := c.session
	if s == nil {
		return
	}
	c.session = nil
	s.client = nil
	s.lastSeen = time.Now()
	h.removeFromQueue(s)
	if s.match != nil {
		h.broadcast(s.match)
	}
}

func (h *Hub) join(s *session, rawName string) {
	name, err := NormalizeName(rawName)
	if err != nil {
		s.client.sendError(err.Error())
		return
	}
	s.name = name
	if m := s.match; m != nil {
		if !m.game.Over() {
			s.client.sendError("finish or leave your current game first")
			return
		}
		h.detach(s)
	}
	if !s.queued {
		s.queued = true
		h.queue = append(h.queue, s)
	}
	h.sendStatus(s)
	h.pairQueue()
}

func (h *Hub) pairQueue() {
	for len(h.queue) >= 2 {
		a, b := h.queue[0], h.queue[1]
		h.queue = slices.Delete(h.queue, 0, 2)
		a.queued, b.queued = false, false
		if mrand.IntN(2) == 1 {
			a, b = b, a
		}
		h.startMatch(a, b)
	}
}

func (h *Hub) removeFromQueue(s *session) {
	if !s.queued {
		return
	}
	s.queued = false
	if i := slices.Index(h.queue, s); i >= 0 {
		h.queue = slices.Delete(h.queue, i, i+1)
	}
}

func (h *Hub) startMatch(x, o *session) {
	m := &match{id: newID(), players: [2]*session{x, o}, game: game.New()}
	x.match, o.match = m, m
	h.resetTimer(m)
	h.broadcast(m)
}

func (h *Hub) move(s *session, x, y int) {
	m := s.match
	if m == nil {
		s.client.sendError("you are not in a game")
		return
	}
	if x < -maxCoord || x > maxCoord || y < -maxCoord || y > maxCoord {
		s.client.sendError("that cell is too far away")
		return
	}
	if err := m.game.Play(seatMark(m.seat(s)), game.Point{X: x, Y: y}); err != nil {
		s.client.sendError(err.Error())
		return
	}
	if m.game.Over() {
		h.stopTimer(m)
	} else {
		h.resetTimer(m)
	}
	h.broadcast(m)
}

func (h *Hub) requestRematch(s *session) {
	m := s.match
	if m == nil || !m.game.Over() {
		s.client.sendError("there is no finished game to rematch")
		return
	}
	seat := m.seat(s)
	if m.left[1-seat] {
		s.client.sendError("your opponent has left")
		return
	}
	m.rematch[seat] = true
	if m.rematch[0] && m.rematch[1] {
		// Swap colours so the other player gets to move first.
		h.startMatch(m.players[1], m.players[0])
		return
	}
	h.broadcast(m)
}

// leave takes the player back to the lobby, resigning a running game.
func (h *Hub) leave(s *session) {
	h.removeFromQueue(s)
	if m := s.match; m != nil {
		if !m.game.Over() {
			m.game.Forfeit(seatMark(m.seat(s)), game.Resign)
			h.stopTimer(m)
		}
		h.detach(s)
	}
	h.sendStatus(s)
}

// detach removes s from its finished match and lets the opponent know.
func (h *Hub) detach(s *session) {
	m := s.match
	seat := m.seat(s)
	m.left[seat] = true
	m.rematch[seat] = false
	s.match = nil
	h.broadcast(m)
}

func (h *Hub) resetTimer(m *match) {
	h.stopTimer(m)
	gen := m.timerGen
	m.deadline = time.Now().Add(h.cfg.TurnTime)
	m.timer = time.AfterFunc(h.cfg.TurnTime, func() { h.turnExpired(m, gen) })
}

func (h *Hub) stopTimer(m *match) {
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	m.timerGen++
}

func (h *Hub) turnExpired(m *match, gen int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if gen != m.timerGen || m.game.Over() {
		return
	}
	m.game.Forfeit(m.game.Turn(), game.Timeout)
	h.stopTimer(m)
	h.broadcast(m)
}

// sendStatus tells a player which screen they belong on.
func (h *Hub) sendStatus(s *session) {
	if s.client == nil {
		return
	}
	switch {
	case s.match != nil:
		s.client.send(h.gameState(s.match, s.match.seat(s)))
	case s.queued:
		s.client.send(protocol.Simple{Type: "queued"})
	default:
		s.client.send(protocol.Simple{Type: "lobby"})
	}
}

// broadcast sends the match state to every connected player still in it.
func (h *Hub) broadcast(m *match) {
	for seat, p := range m.players {
		if p.match == m && p.client != nil {
			p.client.send(h.gameState(m, seat))
		}
	}
}

func (h *Hub) gameState(m *match, seat int) protocol.Game {
	g := m.game
	me, opp := m.players[seat], m.players[1-seat]
	st := protocol.Game{
		Type:              "game",
		ID:                m.id,
		You:               seatMark(seat).String(),
		YourName:          me.name,
		OpponentName:      opp.name,
		OpponentConnected: opp.client != nil,
		Moves:             cells(g.Moves()),
		Turn:              g.Turn().String(),
		TurnMs:            h.cfg.TurnTime.Milliseconds(),
		Over:              g.Over(),
		Rematch:           protocol.Rematch{You: m.rematch[seat], Opponent: m.rematch[1-seat]},
		OpponentLeft:      m.left[1-seat],
	}
	if g.Over() {
		st.Winner = g.Winner().String()
		st.Reason = string(g.Reason())
		st.Line = cells(g.Line())
	} else {
		st.RemainingMs = max(0, time.Until(m.deadline).Milliseconds())
	}
	return st
}

func cells(ps []game.Point) []protocol.Cell {
	out := make([]protocol.Cell, len(ps))
	for i, p := range ps {
		out[i] = protocol.Cell{p.X, p.Y}
	}
	return out
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
