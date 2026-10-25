// Package hub owns all live state: player sessions, the matchmaking queue,
// rooms for playing with friends, and running matches. Everything is kept
// in memory and guarded by a single mutex, which is plenty for a game where
// each action is a few map operations.
package hub

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	mrand "math/rand/v2"
	"slices"
	"strings"
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

	// Room codes are short enough to read out, without look-alike characters
	// (0/o, 1/l/i), and long enough (31^8, about 850 billion) that nobody
	// stumbles into a stranger's room by guessing.
	codeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	codeLength   = 8
)

// How a match came about; sent to clients as Game.Mode. Games against
// Amőbot run entirely in the browser and never reach the server.
const (
	modeRandom = "random" // paired from the matchmaking queue
	modeRoom   = "room"   // friends playing in a room, started by its host
)

// How many players a room's games take.
const (
	roomMin = 2
	roomMax = game.MaxPlayers
)

type Config struct {
	TurnTime time.Duration
	// OriginPatterns lists extra allowed WebSocket origins (host patterns).
	// Same-host origins are always allowed.
	OriginPatterns []string

	// Abuse limits; zero means the default. Per-IP is generous because a whole
	// lecture hall can share one NAT address.
	MaxConns      int // default 5000
	MaxConnsPerIP int // default 50
	MaxSessions   int // default 20000

	// RematchGrace is how long a player who drops off after a game has to come
	// back before the opponent is told no rematch is coming. Default 3s, so a
	// phone's brief connection blip doesn't count as leaving.
	RematchGrace time.Duration
	// GuestGrace is how long a guest who drops off keeps their place in a
	// room. Default 10s.
	GuestGrace time.Duration
	// HostGrace is how long a room waits for its host to come back before it
	// closes. Default 1 minute.
	HostGrace time.Duration
	// StatsInterval is how often the online count is refreshed. Default 1s.
	StatsInterval time.Duration
}

type Hub struct {
	cfg Config

	mu        sync.Mutex
	sessions  map[string]*session // by token
	queue     []*session
	rooms     map[string]*room // by code
	conns     int
	connsByIP map[string]int
	online    int // players in running matches or the queue, as last broadcast
}

// session is a player identity that survives reconnects. The client keeps its
// token in local storage and presents it in hello.
type session struct {
	token    string
	name     string
	client   *client // nil while disconnected
	match    *match  // current or just-finished match; nil in the lobby
	queued   bool
	room     *room // room the player is waiting in
	lastSeen time.Time
}

type match struct {
	id       string
	mode     string
	players  []*session // seat i plays game.SeatMark(i)
	game     *game.Game
	deadline time.Time
	timer    *time.Timer
	timerGen int // invalidates timers that fire after being replaced
	rematch  []bool
	left     []bool
	room     *room // the room a friends game was started from
}

func (m *match) seat(s *session) int { return slices.Index(m.players, s) }

// mark returns the mark s plays in m.
func (m *match) mark(s *session) game.Mark { return game.SeatMark(m.seat(s)) }

// playing reports whether s still has a part in m's running game.
func (m *match) playing(s *session) bool {
	return !m.game.Over() && m.game.Out(m.mark(s)) == ""
}

// room is where friends gather for a game, which its host starts. It lives on
// through its games: newcomers can take a free seat even while one is played
// and join the next, and after a game the players come back to it. It closes
// when the host leaves or stays away too long.
type room struct {
	code    string
	host    *session
	members []*session // waiting for the next game, in joining order; the host first when in it
	match   *match     // the game being played, or the last one
}

func (r *room) playing() bool { return r.match != nil && !r.match.game.Over() }

// seated counts the players with a place in the room: those waiting in it,
// and those still in its last game (or looking at its result).
func (r *room) seated() int {
	n := len(r.members)
	if m := r.match; m != nil {
		for _, p := range m.players {
			if p.match == m && p.room != r {
				n++
			}
		}
	}
	return n
}

func New(cfg Config) *Hub {
	cfg.MaxConns = cmp.Or(cfg.MaxConns, 5000)
	cfg.MaxConnsPerIP = cmp.Or(cfg.MaxConnsPerIP, 50)
	cfg.MaxSessions = cmp.Or(cfg.MaxSessions, 20000)
	cfg.RematchGrace = cmp.Or(cfg.RematchGrace, 3*time.Second)
	cfg.GuestGrace = cmp.Or(cfg.GuestGrace, 10*time.Second)
	cfg.HostGrace = cmp.Or(cfg.HostGrace, time.Minute)
	cfg.StatsInterval = cmp.Or(cfg.StatsInterval, time.Second)
	return &Hub{
		cfg:       cfg,
		sessions:  make(map[string]*session),
		rooms:     make(map[string]*room),
		connsByIP: make(map[string]int),
	}
}

// Run periodically refreshes the online count and forgets sessions that have
// been gone for a while. It returns when ctx is done.
func (h *Hub) Run(ctx context.Context) {
	sweep := time.NewTicker(sweepInterval)
	defer sweep.Stop()
	stats := time.NewTicker(h.cfg.StatsInterval)
	defer stats.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-sweep.C:
			h.sweep(now)
		case <-stats.C:
			h.refreshStats()
		}
	}
}

// refreshStats recounts the players who are playing or waiting for an
// opponent and, when the number changed, tells everyone on the home or
// matchmaking screen. Counting on a
// timer rather than on every change batches bursts into one update.
func (h *Hub) refreshStats() {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, s := range h.sessions {
		if s.queued || (s.match != nil && s.match.playing(s)) {
			n++
		}
	}
	if n == h.online {
		return
	}
	h.online = n
	for _, s := range h.sessions {
		if s.client != nil && s.match == nil {
			h.sendStats(s.client)
		}
	}
}

func (h *Hub) sendStats(c *client) {
	c.send(protocol.Stats{Type: "stats", Online: h.online})
}

func (h *Hub) sweep(now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for token, s := range h.sessions {
		if s.client != nil || now.Sub(s.lastSeen) < sessionTTL {
			continue
		}
		if m := s.match; m != nil {
			if m.playing(s) {
				continue // the turn timer will finish it
			}
			h.detach(s)
		}
		h.leaveRoom(s)
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
	case "room":
		h.createRoom(s, msg.Name)
	case "peekRoom":
		h.peekRoom(s, msg.Code)
	case "enter":
		h.enterRoom(s, msg.Code, msg.Name)
	case "start":
		h.startRoom(s)
	case "toRoom":
		h.returnToRoom(s)
	case "cancel":
		h.removeFromQueue(s)
		h.leaveRoom(s)
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
		if len(h.sessions) >= h.cfg.MaxSessions {
			c.sendError("the server is full, please try again later")
			c.closeSoon()
			return
		}
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
		h.broadcast(s.match) // also tells the opponents we're back
		return
	}
	if s.room != nil {
		h.broadcastRoom(s.room) // tells the others we're back
		return
	}
	h.sendStatus(s)
}

// acquireConn reserves a connection slot for ip, or reports that a limit is hit.
func (h *Hub) acquireConn(ip string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conns >= h.cfg.MaxConns || h.connsByIP[ip] >= h.cfg.MaxConnsPerIP {
		return false
	}
	h.conns++
	h.connsByIP[ip]++
	return true
}

func (h *Hub) releaseConn(ip string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.conns--
	if h.connsByIP[ip]--; h.connsByIP[ip] <= 0 {
		delete(h.connsByIP, ip)
	}
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
	if r := s.room; r != nil {
		h.broadcastRoom(r)
		h.leaveRoomLater(s, r)
	}
	if m := s.match; m != nil {
		h.broadcast(m)
		if !m.playing(s) {
			h.abandonLater(s, m)
		}
	}
}

// prepare sets the player's name before they start looking for a game, and
// leaves a match they no longer play in behind. It reports false, after
// telling the player why, when the name is rejected or they are still
// playing. Callers then leave whatever else the player was waiting in.
func (h *Hub) prepare(s *session, rawName string) bool {
	name, err := NormalizeName(rawName)
	if err != nil {
		h.removeFromQueue(s)
		h.leaveRoom(s)
		s.client.send(protocol.Error{Type: "error", Code: "name", Message: err.Error()})
		return false
	}
	s.name = name
	if m := s.match; m != nil {
		if m.playing(s) {
			s.client.sendError("finish or leave your current game first")
			return false
		}
		h.detach(s)
	}
	return true
}

func (h *Hub) join(s *session, rawName string) {
	if !h.prepare(s, rawName) {
		return
	}
	h.leaveRoom(s)
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
		h.startMatch(modeRandom, nil, a, b)
	}
}

// newCode returns a code that no room uses.
func (h *Hub) newCode() string {
	b := make([]byte, codeLength)
	for {
		_, _ = rand.Read(b)
		for i := range b {
			b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
		}
		if code := string(b); h.rooms[code] == nil {
			return code
		}
	}
}

// createRoom opens a room with the player as its host. Asking again while
// hosting one returns the same room.
func (h *Hub) createRoom(s *session, rawName string) {
	if !h.prepare(s, rawName) {
		return
	}
	h.removeFromQueue(s)
	if r := s.room; r == nil || r.host != s {
		h.leaveRoom(s)
		r = &room{code: h.newCode(), host: s, members: []*session{s}}
		h.rooms[r.code] = r
		s.room = r
	}
	h.broadcastRoom(s.room)
}

// peekRoom tells a player who opened a room link who is in it.
func (h *Hub) peekRoom(s *session, code string) {
	r := h.roomFor(s, code)
	if r == nil {
		return
	}
	names := make([]string, len(r.members))
	for i, p := range r.members {
		names[i] = p.name
	}
	s.client.send(protocol.RoomInvite{
		Type: "roomInvite", Code: r.code, Host: r.host.name, Players: names, Max: roomMax, Playing: r.playing(),
	})
}

// enterRoom adds the player to a room. During a game they wait for the next.
func (h *Hub) enterRoom(s *session, code, rawName string) {
	r := h.roomFor(s, code)
	if r == nil {
		return
	}
	if s.room == r {
		h.sendStatus(s)
		return
	}
	if !h.prepare(s, rawName) {
		return
	}
	h.removeFromQueue(s)
	h.leaveRoom(s)
	r.members = append(r.members, s)
	s.room = r
	h.broadcastRoom(r)
}

// roomFor looks up a room code, telling the player when they can't join it.
func (h *Hub) roomFor(s *session, code string) *room {
	r := h.rooms[strings.ToLower(code)]
	switch {
	case r == nil:
		h.roomError(s, "This room was closed. Ask for a new link.")
		return nil
	case s.room != r && (r.match == nil || s.match != r.match) && r.seated() >= roomMax:
		// Players of the room's last game already have a seat.
		h.roomError(s, "This room is full.")
		return nil
	}
	return r
}

func (h *Hub) roomError(s *session, msg string) {
	if s.client != nil {
		s.client.send(protocol.Error{Type: "error", Code: "room", Message: msg})
	}
}

// startRoom starts the game when the host asks for it.
func (h *Hub) startRoom(s *session) {
	r := s.room
	switch {
	case r == nil || r.host != s:
		s.client.sendError("only the host can start the game")
	case len(r.members) < roomMin:
		s.client.sendError("waiting for more players")
	default:
		h.startGame(r)
	}
}

// startGame seats the room's members in a new match. Newcomers can still
// enter the room meanwhile and wait for the next one.
func (h *Hub) startGame(r *room) {
	players, last := r.members, r.match
	r.members = nil
	for _, p := range players {
		if p.match != nil {
			h.detach(p) // the host, still looking at the last game's result
		}
		p.room = nil
	}
	r.match = h.startMatch(modeRoom, r, players...)
	if last != nil {
		h.broadcast(last) // too late to go back to the room from there
	}
}

// returnToRoom takes a player from a finished friends game back to its room.
func (h *Hub) returnToRoom(s *session) {
	m := s.match
	if m == nil || !m.game.Over() || m.room == nil {
		s.client.sendError("there is no room to go back to")
		return
	}
	r := m.room
	switch {
	case h.rooms[r.code] != r:
		h.roomError(s, "The room was closed.")
		return
	case r.match != m:
		h.roomError(s, "The next game has already started.")
		return
	}
	// The player's seat was kept for them, so there's room. The host is in
	// the room already.
	if s != r.host {
		r.members = append(r.members, s)
		s.room = r
	}
	h.detach(s)
	h.broadcastRoom(r)
}

// leaveRoom takes the player out of their waiting room. When the host
// leaves, the room closes for everyone.
func (h *Hub) leaveRoom(s *session) {
	r := s.room
	if r == nil {
		return
	}
	if r.host != s {
		s.room = nil
		r.members = slices.DeleteFunc(r.members, func(p *session) bool { return p == s })
		h.broadcastRoom(r)
		return
	}
	h.closeRoom(r, r.host.name+" closed the room.")
}

// leaveRoomLater takes a player who dropped off out of their room if they
// haven't come back in time. Guests get a moment for a connection blip;
// without its host for longer, the room closes.
func (h *Hub) leaveRoomLater(s *session, r *room) {
	grace := h.cfg.GuestGrace
	if r.host == s {
		grace = h.cfg.HostGrace
	}
	time.AfterFunc(grace, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		// A player who dropped off again since gets a timer of their own.
		if s.client != nil || s.room != r || time.Since(s.lastSeen) < grace {
			return
		}
		if r.host == s {
			h.closeRoom(r, s.name+" left, so the room was closed.")
		} else {
			h.leaveRoom(s)
		}
	})
}

// closeRoom closes the room for everyone, telling those waiting in it why.
func (h *Hub) closeRoom(r *room, why string) {
	delete(h.rooms, r.code)
	for _, p := range r.members {
		p.room = nil
		if p != r.host {
			h.roomError(p, why)
			h.sendStatus(p)
		}
	}
	r.members = nil
	if r.match != nil {
		h.broadcast(r.match) // nobody can go back to the room any more
	}
}

// broadcastRoom sends the room's state to every connected member who is
// looking at it; the host may still be on the last game's result.
func (h *Hub) broadcastRoom(r *room) {
	members := make([]protocol.RoomMember, len(r.members))
	for i, p := range r.members {
		members[i] = protocol.RoomMember{Name: p.name, Connected: p.client != nil, Host: p == r.host}
	}
	for i, p := range r.members {
		if p.client != nil && p.match == nil {
			p.client.send(protocol.Room{
				Type: "room", Code: r.code, Host: r.host.name, Players: members, You: i,
				Max: roomMax, Min: roomMin, Playing: r.playing(),
			})
		}
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

// startMatch seats players in a new match, in random order. r is the room
// a friends game starts from.
func (h *Hub) startMatch(mode string, r *room, players ...*session) *match {
	players = slices.Clone(players)
	mrand.Shuffle(len(players), func(i, j int) { players[i], players[j] = players[j], players[i] })
	return h.startMatchAs(mode, r, players)
}

// startMatchAs seats players in a new match in the given order, so the
// first plays X and moves first.
func (h *Hub) startMatchAs(mode string, r *room, players []*session) *match {
	n := len(players)
	m := &match{
		id: newID(), mode: mode, players: players, game: game.New(n),
		rematch: make([]bool, n), left: make([]bool, n), room: r,
	}
	for _, p := range players {
		p.match = m
	}
	h.resetTimer(m)
	h.broadcast(m)
	return m
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
	if err := m.game.Play(m.mark(s), game.Point{X: x, Y: y}); err != nil {
		s.client.sendError(err.Error())
		return
	}
	if m.game.Over() {
		h.gameEnded(m)
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
	if m.room != nil {
		s.client.sendError("go back to the room for the next game")
		return
	}
	var stayed []*session
	for seat, p := range m.players {
		if !m.left[seat] {
			stayed = append(stayed, p)
		}
	}
	if len(stayed) < 2 {
		s.client.sendError("not enough players are left for a rematch")
		return
	}
	m.rematch[m.seat(s)] = true
	for _, p := range stayed {
		if !m.rematch[m.seat(p)] {
			h.broadcast(m)
			return
		}
	}
	// Rotate the seats so the next player gets to move first. With two
	// players, that swaps colours.
	h.startMatchAs(m.mode, nil, append(stayed[1:], stayed[0]))
}

// leave takes the player back to the lobby, resigning a running game.
func (h *Hub) leave(s *session) {
	h.removeFromQueue(s)
	h.leaveRoom(s)
	if m := s.match; m != nil {
		h.forfeit(m, m.mark(s), game.Resign)
		h.detach(s)
	}
	h.sendStatus(s)
}

// forfeit takes mark out of the running game in m, which ends it when only
// one player is left.
func (h *Hub) forfeit(m *match, mark game.Mark, reason game.Reason) {
	g := m.game
	if g.Over() || g.Out(mark) != "" {
		return
	}
	wasTurn := g.Turn() == mark
	g.Forfeit(mark, reason)
	switch {
	case g.Over():
		h.gameEnded(m)
	case wasTurn:
		h.resetTimer(m)
	}
}

// detach removes s from a match they no longer play in and lets the others
// know.
func (h *Hub) detach(s *session) {
	m := s.match
	seat := m.seat(s)
	m.left[seat] = true
	m.rematch[seat] = false
	s.match = nil
	if r := m.room; r != nil && r.host == s && s.room != r && h.rooms[r.code] == r {
		h.closeRoom(r, s.name+" closed the room.") // left the game rather than going back to the room
	}
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
	h.forfeit(m, m.game.Turn(), game.Timeout)
	h.broadcast(m)
}

// gameEnded stops the clock of a match that just finished. A player who is
// already gone (e.g. closed the page and then lost on time) won't be back for
// a rematch either.
func (h *Hub) gameEnded(m *match) {
	h.stopTimer(m)
	if r := m.room; r != nil && h.rooms[r.code] == r {
		// The host is back in the room, ahead of anyone who entered during
		// the game; the others come back when they're ready.
		r.members = append([]*session{r.host}, r.members...)
		r.host.room = r
		if r.host.client == nil {
			h.leaveRoomLater(r.host, r)
		}
		h.broadcastRoom(r)
	}
	for _, p := range m.players {
		if p.client == nil && p.match == m {
			h.abandonLater(p, m)
		}
	}
}

// abandonLater takes a disconnected player out of their finished match once
// the grace period passes without them coming back, which tells the opponent
// that the rematch is off.
func (h *Hub) abandonLater(s *session, m *match) {
	time.AfterFunc(h.cfg.RematchGrace, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if s.client == nil && s.match == m {
			h.detach(s)
		}
	})
}

// sendStatus tells a player which screen they belong on.
func (h *Hub) sendStatus(s *session) {
	if s.client == nil {
		return
	}
	switch {
	case s.match != nil:
		s.client.send(h.gameState(s.match, s.match.seat(s)))
	case s.room != nil:
		h.broadcastRoom(s.room)
	case s.queued:
		s.client.send(protocol.Simple{Type: "queued"})
		h.sendStats(s.client)
	default:
		s.client.send(protocol.Simple{Type: "lobby"})
		h.sendStats(s.client)
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
	players := make([]protocol.Player, len(m.players))
	for i, p := range m.players {
		players[i] = protocol.Player{
			Name:      p.name,
			Mark:      game.SeatMark(i).String(),
			Connected: p.client != nil,
			Out:       string(g.Out(game.SeatMark(i))),
			Left:      m.left[i],
			Rematch:   m.rematch[i],
		}
	}
	moves := make([]protocol.Move, len(g.Moves()))
	for i, mv := range g.Moves() {
		moves[i] = protocol.Move{mv.X, mv.Y, int(mv.Mark) - 1}
	}
	st := protocol.Game{
		Type:    "game",
		ID:      m.id,
		Mode:    m.mode,
		You:     seat,
		Players: players,
		Moves:   moves,
		Turn:    g.Turn().String(),
		TurnMs:  h.cfg.TurnTime.Milliseconds(),
		Over:    g.Over(),
	}
	if r := m.room; r != nil && h.rooms[r.code] == r && r.match == m {
		st.Room = r.code
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
