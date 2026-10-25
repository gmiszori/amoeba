// Package hub owns all live state: player sessions, the matchmaking queue,
// challenge links, multiplayer rooms and running matches. Everything is kept in memory and guarded by a single mutex,
// which is plenty for a game where each action is a few map operations.
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
	// kept so a returning player can pick up where they left off. A pending
	// challenge link lives as long as its session.
	sessionTTL    = 10 * time.Minute
	sweepInterval = time.Minute

	// Challenge and room codes are short enough to read out, without
	// look-alike characters (0/o, 1/l/i).
	codeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	codeLength   = 6
)

// How a match came about; sent to clients as Game.Mode. Games against
// Amőbot run entirely in the browser and never reach the server.
const (
	modeRandom = "random" // paired from the matchmaking queue
	modeFriend = "friend" // one player opened the other's challenge link
	modeRoom   = "room"   // a multiplayer game started from a room link
)

// How many players a multiplayer room takes. It starts by itself when full.
const (
	roomMin = 3
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
	// StatsInterval is how often the online count is refreshed. Default 1s.
	StatsInterval time.Duration
}

type Hub struct {
	cfg Config

	mu         sync.Mutex
	sessions   map[string]*session // by token
	queue      []*session
	challenges map[string]*session // by code
	rooms      map[string]*room    // by code
	conns      int
	connsByIP  map[string]int
	online     int // players in running matches or the queue, as last broadcast
}

// session is a player identity that survives reconnects. The client keeps its
// token in local storage and presents it in hello.
type session struct {
	token     string
	name      string
	client    *client // nil while disconnected
	match     *match  // current or just-finished match; nil in the lobby
	queued    bool
	challenge string // code of the challenge link waiting for a friend
	room      *room  // waiting room the player is in
	lastSeen  time.Time
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
}

func (m *match) seat(s *session) int { return slices.Index(m.players, s) }

// mark returns the mark s plays in m.
func (m *match) mark(s *session) game.Mark { return game.SeatMark(m.seat(s)) }

// playing reports whether s still has a part in m's running game.
func (m *match) playing(s *session) bool {
	return !m.game.Over() && m.game.Out(m.mark(s)) == ""
}

// room gathers players for a multiplayer game until the host starts it.
type room struct {
	code    string
	members []*session // in joining order; members[0] is the host
}

func New(cfg Config) *Hub {
	cfg.MaxConns = cmp.Or(cfg.MaxConns, 5000)
	cfg.MaxConnsPerIP = cmp.Or(cfg.MaxConnsPerIP, 50)
	cfg.MaxSessions = cmp.Or(cfg.MaxSessions, 20000)
	cfg.RematchGrace = cmp.Or(cfg.RematchGrace, 3*time.Second)
	cfg.StatsInterval = cmp.Or(cfg.StatsInterval, time.Second)
	return &Hub{
		cfg:        cfg,
		sessions:   make(map[string]*session),
		challenges: make(map[string]*session),
		rooms:      make(map[string]*room),
		connsByIP:  make(map[string]int),
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
		h.cancelChallenge(s)
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
	case "challenge":
		h.createChallenge(s, msg.Name)
	case "peek":
		h.peek(s, msg.Code)
	case "accept":
		h.accept(s, msg.Code, msg.Name)
	case "room":
		h.createRoom(s, msg.Name)
	case "peekRoom":
		h.peekRoom(s, msg.Code)
	case "enter":
		h.enterRoom(s, msg.Code, msg.Name)
	case "start":
		h.startRoom(s)
	case "cancel":
		h.removeFromQueue(s)
		h.cancelChallenge(s)
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
		h.broadcastRoom(s.room) // the host is back
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
		// The host's room waits for them to come back; anyone else is out,
		// and the client enters again when it reconnects.
		if r.members[0] != s {
			h.leaveRoom(s)
		} else {
			h.broadcastRoom(r)
		}
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
		h.cancelChallenge(s)
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
	h.cancelChallenge(s)
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
		h.startMatch(modeRandom, a, b)
	}
}

// createChallenge gives the player a link to send to a friend. Asking again
// while waiting returns the same link.
func (h *Hub) createChallenge(s *session, rawName string) {
	if !h.prepare(s, rawName) {
		return
	}
	h.removeFromQueue(s)
	h.leaveRoom(s)
	if s.challenge == "" {
		s.challenge = h.newCode()
		h.challenges[s.challenge] = s
	}
	h.sendStatus(s)
}

// peek tells a player who opened a challenge link who is waiting behind it.
func (h *Hub) peek(s *session, code string) {
	if from := h.challengeFor(s, code); from != nil {
		s.client.send(protocol.Invite{Type: "invite", Code: from.challenge, Name: from.name})
	}
}

// accept starts a game between the player and whoever sent the challenge.
func (h *Hub) accept(s *session, code, rawName string) {
	from := h.challengeFor(s, code)
	if from == nil || !h.prepare(s, rawName) {
		return
	}
	if from.client == nil {
		h.challengeError(s, from.name+" isn't online right now. Try again in a moment.")
		return
	}
	h.removeFromQueue(s)
	h.cancelChallenge(s)
	h.leaveRoom(s)
	h.cancelChallenge(from)
	h.startMatch(modeFriend, s, from)
}

// challengeFor looks up a challenge code, telling the player when it can't be
// used.
func (h *Hub) challengeFor(s *session, code string) *session {
	from := h.challenges[strings.ToLower(code)]
	switch from {
	case nil:
		h.challengeError(s, "This challenge has expired. Ask your friend for a new link.")
	case s:
		h.challengeError(s, "That's your own challenge link. Send it to a friend!")
		return nil
	}
	return from
}

func (h *Hub) challengeError(s *session, msg string) {
	s.client.send(protocol.Error{Type: "error", Code: "challenge", Message: msg})
}

func (h *Hub) cancelChallenge(s *session) {
	if s.challenge != "" {
		delete(h.challenges, s.challenge)
		s.challenge = ""
	}
}

// newCode returns a code that no challenge link or room uses.
func (h *Hub) newCode() string {
	b := make([]byte, codeLength)
	for {
		_, _ = rand.Read(b)
		for i := range b {
			b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
		}
		if code := string(b); h.challenges[code] == nil && h.rooms[code] == nil {
			return code
		}
	}
}

// createRoom opens a multiplayer room with the player as its host. Asking
// again while hosting one returns the same room.
func (h *Hub) createRoom(s *session, rawName string) {
	if !h.prepare(s, rawName) {
		return
	}
	h.removeFromQueue(s)
	h.cancelChallenge(s)
	if r := s.room; r == nil || r.members[0] != s {
		h.leaveRoom(s)
		r = &room{code: h.newCode(), members: []*session{s}}
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
	s.client.send(protocol.RoomInvite{Type: "roomInvite", Code: r.code, Players: names, Max: roomMax})
}

// enterRoom adds the player to a room, which starts the game once it's full.
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
	h.cancelChallenge(s)
	h.leaveRoom(s)
	r.members = append(r.members, s)
	s.room = r
	if len(r.members) == roomMax {
		h.startGame(r)
		return
	}
	h.broadcastRoom(r)
}

// roomFor looks up a room code, telling the player when they can't join it.
func (h *Hub) roomFor(s *session, code string) *room {
	r := h.rooms[strings.ToLower(code)]
	switch {
	case r == nil:
		h.roomError(s, "This game has already started or was closed. Ask for a new link.")
		return nil
	case s.room != r && len(r.members) >= roomMax:
		h.roomError(s, "This game is full.")
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
	case r == nil || r.members[0] != s:
		s.client.sendError("only the host can start the game")
	case len(r.members) < roomMin:
		s.client.sendError("waiting for more players")
	default:
		h.startGame(r)
	}
}

// startGame closes the room and seats its members in a new match.
func (h *Hub) startGame(r *room) {
	delete(h.rooms, r.code)
	for _, p := range r.members {
		p.room = nil
	}
	h.startMatch(modeRoom, r.members...)
}

// leaveRoom takes the player out of their waiting room. When the host
// leaves, the room closes for everyone.
func (h *Hub) leaveRoom(s *session) {
	r := s.room
	if r == nil {
		return
	}
	s.room = nil
	if r.members[0] != s {
		r.members = slices.DeleteFunc(r.members, func(p *session) bool { return p == s })
		h.broadcastRoom(r)
		return
	}
	delete(h.rooms, r.code)
	for _, p := range r.members[1:] {
		p.room = nil
		h.roomError(p, s.name+" closed the game.")
		h.sendStatus(p)
	}
}

// broadcastRoom sends the room's state to every connected member.
func (h *Hub) broadcastRoom(r *room) {
	members := make([]protocol.RoomMember, len(r.members))
	for i, p := range r.members {
		members[i] = protocol.RoomMember{Name: p.name, Connected: p.client != nil}
	}
	for i, p := range r.members {
		if p.client != nil {
			p.client.send(protocol.Room{Type: "room", Code: r.code, Players: members, You: i, Max: roomMax, Min: roomMin})
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

// startMatch seats players in a new match, in random order.
func (h *Hub) startMatch(mode string, players ...*session) {
	players = slices.Clone(players)
	mrand.Shuffle(len(players), func(i, j int) { players[i], players[j] = players[j], players[i] })
	h.startMatchAs(mode, players)
}

// startMatchAs seats players in a new match in the given order, so the
// first plays X and moves first.
func (h *Hub) startMatchAs(mode string, players []*session) {
	n := len(players)
	m := &match{
		id: newID(), mode: mode, players: players, game: game.New(n),
		rematch: make([]bool, n), left: make([]bool, n),
	}
	for _, p := range players {
		p.match = m
	}
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
	var stayed []*session
	for seat, p := range m.players {
		if !m.left[seat] {
			stayed = append(stayed, p)
		}
	}
	need := 2
	if m.mode == modeRoom {
		need = roomMin
	}
	if len(stayed) < need {
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
	h.startMatchAs(m.mode, append(stayed[1:], stayed[0]))
}

// leave takes the player back to the lobby, resigning a running game.
func (h *Hub) leave(s *session) {
	h.removeFromQueue(s)
	h.cancelChallenge(s)
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
	case s.challenge != "":
		s.client.send(protocol.Challenge{Type: "challenge", Code: s.challenge})
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
