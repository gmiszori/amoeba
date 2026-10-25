package hub

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/gmiszori/amoeba/server/internal/protocol"
)

// message is the union of every outbound message's fields, except
// roomInvite's (see testClient.roomInvite).
type message struct {
	protocol.Game
	Token   string `json:"token"`
	Name    string `json:"name"`
	Message string `json:"message"`
	Code    string `json:"code"`
	Online  int    `json:"online"`
	Max     int    `json:"max"`
	Min     int    `json:"min"`
}

// me returns the receiving player's seat in a game.
func (m message) me() protocol.Player { return m.Players[m.You] }

// opp returns the other player in a two-player game.
func (m message) opp() protocol.Player { return m.Players[1-m.You] }

type testClient struct {
	t    *testing.T
	conn *websocket.Conn
}

func startServer(t *testing.T, turn time.Duration) string {
	t.Helper()
	return startHub(t, Config{TurnTime: turn})
}

// startHub runs a hub with fast stats and rematch timers unless cfg sets them.
func startHub(t *testing.T, cfg Config) string {
	t.Helper()
	cfg.StatsInterval = cmp.Or(cfg.StatsInterval, 50*time.Millisecond)
	cfg.RematchGrace = cmp.Or(cfg.RematchGrace, 100*time.Millisecond)
	h := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	go h.Run(ctx)
	srv := httptest.NewServer(h)
	t.Cleanup(func() {
		srv.Close()
		cancel()
	})
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func dial(t *testing.T, url string) *testClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return &testClient{t: t, conn: conn}
}

func (c *testClient) send(v any) {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, c.conn, v); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

// expect reads messages until one of type typ satisfies ok.
func (c *testClient) expect(typ string, ok func(message) bool) message {
	c.t.Helper()
	for {
		var m message
		c.decode(c.next(typ), &m)
		if ok == nil || ok(m) {
			return m
		}
	}
}

func (c *testClient) roomInvite() protocol.RoomInvite {
	c.t.Helper()
	var inv protocol.RoomInvite
	c.decode(c.next("roomInvite"), &inv)
	return inv
}

// next reads messages until one of type typ arrives, and returns it.
func (c *testClient) next(typ string) []byte {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, c.conn, &raw); err != nil {
			c.t.Fatalf("waiting for %q: %v", typ, err)
		}
		var head struct{ Type string }
		c.decode(raw, &head)
		if head.Type == typ {
			return raw
		}
	}
}

func (c *testClient) decode(raw []byte, v any) {
	c.t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		c.t.Fatalf("decoding %s: %v", raw, err)
	}
}

func (c *testClient) hello(token string) string {
	c.t.Helper()
	c.send(protocol.Inbound{Type: "hello", Token: token})
	return c.expect("welcome", nil).Token
}

func (c *testClient) move(x, y int) {
	c.send(protocol.Inbound{Type: "move", X: x, Y: y})
}

// matchPair connects two players and returns them ordered as X, O along
// with their session tokens.
func matchPair(t *testing.T, url string) (x, o *testClient, xToken, oToken string) {
	t.Helper()
	a, b := dial(t, url), dial(t, url)
	aTok, bTok := a.hello(""), b.hello("")
	a.send(protocol.Inbound{Type: "join", Name: "alice"})
	a.expect("queued", nil)
	b.send(protocol.Inbound{Type: "join", Name: "bob"})
	ga := a.expect("game", nil)
	gb := b.expect("game", nil)
	if ga.ID != gb.ID || ga.You == gb.You {
		t.Fatalf("bad pairing: %+v / %+v", ga.Game, gb.Game)
	}
	if ga.You == 0 {
		return a, b, aTok, bTok
	}
	return b, a, bTok, aTok
}

func TestMatchAndWin(t *testing.T) {
	x, o, _, _ := matchPair(t, startServer(t, time.Minute))
	for i := range 4 {
		x.move(i, 0)
		o.expect("game", func(m message) bool { return len(m.Moves) == 2*i+1 })
		o.move(i, 1)
		x.expect("game", func(m message) bool { return len(m.Moves) == 2*i+2 })
	}
	x.move(4, 0)
	for _, c := range []*testClient{x, o} {
		g := c.expect("game", func(m message) bool { return m.Over })
		if g.Winner != "X" || g.Reason != "five" || len(g.Line) != 5 {
			t.Fatalf("game over state: %+v", g.Game)
		}
	}
}

func TestMoveOutOfTurnIsRejected(t *testing.T) {
	_, o, _, _ := matchPair(t, startServer(t, time.Minute))
	o.move(0, 0)
	if e := o.expect("error", nil); !strings.Contains(e.Message, "turn") {
		t.Fatalf("unexpected error: %q", e.Message)
	}
}

func TestTurnTimeout(t *testing.T) {
	x, _, _, _ := matchPair(t, startServer(t, 150*time.Millisecond))
	g := x.expect("game", func(m message) bool { return m.Over })
	if g.Winner != "O" || g.Reason != "timeout" {
		t.Fatalf("got winner=%q reason=%q, want O by timeout", g.Winner, g.Reason)
	}
}

func TestRematchSwapsColours(t *testing.T) {
	x, o, _, _ := matchPair(t, startServer(t, 150*time.Millisecond))
	first := x.expect("game", func(m message) bool { return m.Over })
	o.expect("game", func(m message) bool { return m.Over })

	x.send(protocol.Inbound{Type: "rematch"})
	o.expect("game", func(m message) bool { return m.Over && m.opp().Rematch })
	o.send(protocol.Inbound{Type: "rematch"})

	g := x.expect("game", func(m message) bool { return m.ID != first.ID })
	if g.You != 1 || g.Over {
		t.Fatalf("rematch state for old X: %+v", g.Game)
	}
}

func TestReconnectResumesGame(t *testing.T) {
	url := startServer(t, time.Minute)
	x, o, xToken, _ := matchPair(t, url)
	x.move(0, 0)
	g := o.expect("game", func(m message) bool { return len(m.Moves) == 1 })

	x.conn.Close(websocket.StatusNormalClosure, "")
	o.expect("game", func(m message) bool { return !m.opp().Connected })

	x2 := dial(t, url)
	if tok := x2.hello(xToken); tok != xToken {
		t.Fatalf("reconnect got a new token")
	}
	resumed := x2.expect("game", nil)
	if resumed.ID != g.ID || resumed.You != 0 || len(resumed.Moves) != 1 {
		t.Fatalf("resumed state: %+v", resumed.Game)
	}
	o.expect("game", func(m message) bool { return m.opp().Connected })
}

func TestLeaveResigns(t *testing.T) {
	x, o, _, _ := matchPair(t, startServer(t, time.Minute))
	o.send(protocol.Inbound{Type: "leave"})
	o.expect("lobby", nil)
	g := x.expect("game", func(m message) bool { return m.Over })
	if g.Winner != "X" || g.Reason != "resign" || !g.opp().Left {
		t.Fatalf("after opponent left: %+v", g.Game)
	}
	x.send(protocol.Inbound{Type: "rematch"})
	x.expect("error", nil)
}

func TestNormalizeName(t *testing.T) {
	valid := map[string]string{
		"  Gergő  ":             "Gergő",
		"a   b":                 "a b",
		"x_y-z.9":               "x_y-z.9",
		strings.Repeat("é", 16): strings.Repeat("é", 16),
		"Zoë\tKovács":           "Zoë Kovács",
	}
	for in, want := range valid {
		if got, err := NormalizeName(in); err != nil || got != want {
			t.Errorf("NormalizeName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "   ", strings.Repeat("a", 17), "<script>", "emoji😀", "a/b"} {
		if _, err := NormalizeName(in); err == nil {
			t.Errorf("NormalizeName(%q) accepted an invalid name", in)
		}
	}
}

func TestOffensiveNames(t *testing.T) {
	blocked := []string{
		"fuck", "FuCk you", "f.u.c.k", "f u c k", "fuuuuck", "sh1t", "5hit", "s1ut",
		"big ass", "assss", "Kurva", "köcsög", "kocsog", "geci123", "faszfej", "Szar",
		"buzi", "baszódj", "Hitler", "x_dick_x", "bazdmeg",
	}
	for _, name := range blocked {
		if _, err := NormalizeName(name); !errors.Is(err, ErrOffensiveName) {
			t.Errorf("NormalizeName(%q) = %v; want ErrOffensiveName", name, err)
		}
	}
	// Innocent names that contain a short blocked word inside another word.
	allowed := []string{
		"As", "Class", "Hancock", "Dickens", "Szarvas", "Szarka Anna", "Buzinkay",
		"Assisi", "Cassandra", "Grape", "Sussex", "Babaszoba", "Gergő", "Pass",
	}
	for _, name := range allowed {
		if _, err := NormalizeName(name); err != nil {
			t.Errorf("NormalizeName(%q) = %v; want it allowed", name, err)
		}
	}
}

func TestBadNameIsRejectedWithCode(t *testing.T) {
	c := dial(t, startServer(t, time.Minute))
	c.hello("")
	c.send(protocol.Inbound{Type: "join", Name: "fuck"})
	if e := c.expect("error", nil); e.Code != "name" {
		t.Fatalf("error code %q, want name", e.Code)
	}
}

func TestMessageFloodDisconnects(t *testing.T) {
	c := dial(t, startServer(t, time.Minute))
	c.hello("")
	for range 3 * msgBurst {
		c.send(protocol.Inbound{Type: "cancel"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		if _, _, err := c.conn.Read(ctx); err != nil {
			if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
				t.Fatalf("connection ended with %v, want policy violation", err)
			}
			return
		}
	}
}

func TestConnectionLimitPerIP(t *testing.T) {
	srv := httptest.NewServer(New(Config{TurnTime: time.Minute, MaxConnsPerIP: 2}))
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	dial(t, url)
	dial(t, url)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if conn, resp, err := websocket.Dial(ctx, url, nil); err == nil {
		conn.CloseNow()
		t.Fatal("third connection from the same IP was accepted")
	} else if resp == nil || resp.StatusCode != 429 {
		t.Fatalf("third connection: %v, want HTTP 429", err)
	}
}

func TestSessionLimit(t *testing.T) {
	srv := httptest.NewServer(New(Config{TurnTime: time.Minute, MaxSessions: 1}))
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	dial(t, url).hello("")
	c := dial(t, url)
	c.send(protocol.Inbound{Type: "hello"})
	if e := c.expect("error", nil); !strings.Contains(e.Message, "full") {
		t.Fatalf("unexpected error %q", e.Message)
	}
}

func TestZalgoNameRejected(t *testing.T) {
	if _, err := NormalizeName("á̂̃b"); err == nil {
		t.Fatal("name with three stacked combining marks was accepted")
	}
	if _, err := NormalizeName("Gergő"); err != nil { // decomposed ő
		t.Fatalf("decomposed accent rejected: %v", err)
	}
}

func TestOnlineCount(t *testing.T) {
	url := startServer(t, time.Minute)
	watcher := dial(t, url)
	watcher.hello("")
	if st := watcher.expect("stats", nil); st.Online != 0 {
		t.Fatalf("initial online count %d, want 0", st.Online)
	}

	waiting := dial(t, url)
	waiting.hello("")
	waiting.send(protocol.Inbound{Type: "join", Name: "carol"})
	watcher.expect("stats", func(m message) bool { return m.Online == 1 }) // in the queue

	waiting.send(protocol.Inbound{Type: "cancel"})
	watcher.expect("stats", func(m message) bool { return m.Online == 0 })

	x, _, _, _ := matchPair(t, url)
	watcher.expect("stats", func(m message) bool { return m.Online == 2 })
	x.send(protocol.Inbound{Type: "leave"}) // resigns; the game is over
	watcher.expect("stats", func(m message) bool { return m.Online == 0 })
}

func TestRematchCancelledWhenOpponentLeavesSite(t *testing.T) {
	x, o, _, _ := matchPair(t, startServer(t, 150*time.Millisecond))
	o.expect("game", func(m message) bool { return m.Over })
	o.send(protocol.Inbound{Type: "rematch"})
	o.expect("game", func(m message) bool { return m.me().Rematch })

	x.conn.Close(websocket.StatusNormalClosure, "") // closes the page
	g := o.expect("game", func(m message) bool { return m.opp().Left })
	if !g.me().Rematch {
		t.Fatal("lost track of our own rematch request")
	}
	o.send(protocol.Inbound{Type: "rematch"})
	o.expect("error", nil)
}

func TestBriefDropKeepsRematch(t *testing.T) {
	url := startHub(t, Config{TurnTime: 150 * time.Millisecond, RematchGrace: time.Second})
	x, o, xToken, _ := matchPair(t, url)
	first := o.expect("game", func(m message) bool { return m.Over })

	x.conn.Close(websocket.StatusNormalClosure, "")
	o.expect("game", func(m message) bool { return !m.opp().Connected })
	x2 := dial(t, url)
	x2.hello(xToken)
	x2.expect("game", func(m message) bool { return m.ID == first.ID })
	time.Sleep(1200 * time.Millisecond) // past the grace period

	x2.send(protocol.Inbound{Type: "rematch"})
	o.expect("game", func(m message) bool { return m.opp().Rematch && !m.opp().Left })
	o.send(protocol.Inbound{Type: "rematch"})
	o.expect("game", func(m message) bool { return m.ID != first.ID })
}

func TestChallengeLink(t *testing.T) {
	url := startServer(t, time.Minute)
	a, b := dial(t, url), dial(t, url)
	a.hello("")
	b.hello("")

	a.send(protocol.Inbound{Type: "challenge", Name: "alice"})
	code := a.expect("challenge", nil).Code
	if len(code) != codeLength {
		t.Fatalf("challenge code %q", code)
	}

	b.send(protocol.Inbound{Type: "peek", Code: strings.ToUpper(code)})
	if inv := b.expect("invite", nil); inv.Name != "alice" || inv.Code != code {
		t.Fatalf("invite: %+v", inv)
	}

	b.send(protocol.Inbound{Type: "accept", Code: code, Name: "bob"})
	ga := a.expect("game", nil)
	gb := b.expect("game", nil)
	if ga.ID != gb.ID || ga.Mode != "friend" || ga.opp().Name != "bob" || gb.opp().Name != "alice" {
		t.Fatalf("challenge game: %+v / %+v", ga.Game, gb.Game)
	}

	// The link is single-use.
	c := dial(t, url)
	c.hello("")
	c.send(protocol.Inbound{Type: "peek", Code: code})
	if e := c.expect("error", nil); e.Code != "challenge" {
		t.Fatalf("reused link: %+v", e)
	}
}

func TestChallengeRejections(t *testing.T) {
	url := startServer(t, time.Minute)
	a, b := dial(t, url), dial(t, url)
	aTok := a.hello("")
	b.hello("")

	b.send(protocol.Inbound{Type: "peek", Code: "nope42"})
	if e := b.expect("error", nil); e.Code != "challenge" || !strings.Contains(e.Message, "expired") {
		t.Fatalf("unknown code: %+v", e)
	}

	a.send(protocol.Inbound{Type: "challenge", Name: "alice"})
	code := a.expect("challenge", nil).Code
	a.send(protocol.Inbound{Type: "peek", Code: code})
	if e := a.expect("error", nil); !strings.Contains(e.Message, "your own") {
		t.Fatalf("own link: %+v", e)
	}

	// The link outlives a dropped connection, but can't be accepted until
	// its owner is back.
	a.conn.Close(websocket.StatusNormalClosure, "")
	time.Sleep(100 * time.Millisecond)
	b.send(protocol.Inbound{Type: "accept", Code: code, Name: "bob"})
	if e := b.expect("error", nil); !strings.Contains(e.Message, "isn't online") {
		t.Fatalf("offline challenger: %+v", e)
	}
	a2 := dial(t, url)
	a2.hello(aTok)
	if got := a2.expect("challenge", nil).Code; got != code {
		t.Fatalf("after reconnect got code %q, want %q", got, code)
	}

	a2.send(protocol.Inbound{Type: "cancel"})
	a2.expect("lobby", nil)
	b.send(protocol.Inbound{Type: "accept", Code: code, Name: "bob"})
	if e := b.expect("error", nil); e.Code != "challenge" {
		t.Fatalf("cancelled link: %+v", e)
	}
}

// openRoom has a host open a room and the given guests enter it. It returns
// everyone, host first, and the room code.
func openRoom(t *testing.T, url string, guests ...string) ([]*testClient, string) {
	t.Helper()
	host := dial(t, url)
	host.hello("")
	host.send(protocol.Inbound{Type: "room", Name: "host"})
	code := host.expect("room", nil).Code
	clients := []*testClient{host}
	for _, name := range guests {
		c := dial(t, url)
		c.hello("")
		c.send(protocol.Inbound{Type: "enter", Code: code, Name: name})
		clients = append(clients, c)
	}
	return clients, code
}

// bySeat orders the players of a freshly started game by seat.
func bySeat(t *testing.T, clients []*testClient) []*testClient {
	t.Helper()
	seats := make([]*testClient, len(clients))
	var id string
	for _, c := range clients {
		g := c.expect("game", nil)
		if id != "" && g.ID != id {
			t.Fatalf("players ended up in different games")
		}
		id = g.ID
		seats[g.You] = c
	}
	return seats
}

func TestRoomGame(t *testing.T) {
	url := startServer(t, 150*time.Millisecond)
	cs, code := openRoom(t, url, "bob")
	host := cs[0]
	host.expect("room", func(m message) bool { return len(m.Players) == 2 })

	peeker := dial(t, url)
	peeker.hello("")
	peeker.send(protocol.Inbound{Type: "peekRoom", Code: strings.ToUpper(code)})
	if inv := peeker.roomInvite(); inv.Code != code || len(inv.Players) != 2 || inv.Players[0] != "host" {
		t.Fatalf("room invite: %+v", inv)
	}

	cs[1].send(protocol.Inbound{Type: "start"})
	if e := cs[1].expect("error", nil); !strings.Contains(e.Message, "host") {
		t.Fatalf("guest starting: %+v", e)
	}
	host.send(protocol.Inbound{Type: "start"})
	if e := host.expect("error", nil); !strings.Contains(e.Message, "more players") {
		t.Fatalf("starting with two: %+v", e)
	}

	peeker.send(protocol.Inbound{Type: "enter", Code: code, Name: "carol"})
	host.expect("room", func(m message) bool { return len(m.Players) == 3 })
	host.send(protocol.Inbound{Type: "start"})
	seats := bySeat(t, append(cs, peeker))

	// Nobody new gets in while the game runs.
	late := dial(t, url)
	late.hello("")
	late.send(protocol.Inbound{Type: "peekRoom", Code: code})
	if e := late.expect("error", nil); e.Code != "room" || !strings.Contains(e.Message, "in progress") {
		t.Fatalf("room during a game: %+v", e)
	}

	// Nobody moves: X and then O run out of time and drop out, and the
	// third player is the last one left.
	g := seats[2].expect("game", func(m message) bool { return m.Players[0].Out != "" })
	if g.Over || g.Turn != "O" || g.Players[0].Out != "timeout" || g.Mode != "room" {
		t.Fatalf("after X timed out: %+v", g.Game)
	}
	g = seats[2].expect("game", func(m message) bool { return m.Over })
	if g.Winner != "T" || g.Reason != "timeout" || g.Room != code {
		t.Fatalf("game over: %+v", g.Game)
	}

	// No rematch in a group game: the players go back to the room, where a
	// newcomer can take the free seat.
	for _, c := range seats[:2] {
		c.expect("game", func(m message) bool { return m.Over })
	}
	seats[0].send(protocol.Inbound{Type: "rematch"})
	seats[0].expect("error", nil)
	guest := seats[0]
	if guest == host {
		guest = seats[1]
	}
	guest.send(protocol.Inbound{Type: "toRoom"})
	r := guest.expect("room", nil)
	if len(r.Players) != 2 || r.Players[0].Name != "host" || r.You != 1 {
		t.Fatalf("back in the room: %+v", r)
	}
	late.send(protocol.Inbound{Type: "enter", Code: code, Name: "dave"})
	late.expect("room", func(m message) bool { return len(m.Players) == 3 })
	host.send(protocol.Inbound{Type: "toRoom"})
	host.expect("room", func(m message) bool { return len(m.Players) == 3 && m.You == 0 })
	host.send(protocol.Inbound{Type: "start"})
	next := late.expect("game", nil)
	if next.ID == g.ID || len(next.Players) != 3 || next.Room != "" {
		t.Fatalf("next game: %+v", next.Game)
	}
}

func TestRoomClosesWhenHostLeavesAfterGame(t *testing.T) {
	url := startServer(t, 150*time.Millisecond)
	cs, code := openRoom(t, url, "bob", "carol")
	cs[0].expect("room", func(m message) bool { return len(m.Players) == 3 })
	cs[0].send(protocol.Inbound{Type: "start"})
	for _, c := range cs {
		c.expect("game", func(m message) bool { return m.Over && m.Room == code })
	}
	cs[1].send(protocol.Inbound{Type: "toRoom"})
	cs[1].expect("room", nil)

	cs[0].send(protocol.Inbound{Type: "leave"}) // the host goes back to the menu
	if e := cs[1].expect("error", nil); e.Code != "room" || !strings.Contains(e.Message, "closed") {
		t.Fatalf("waiting guest: %+v", e)
	}
	cs[1].expect("lobby", nil)
	cs[2].expect("game", func(m message) bool { return m.Room == "" })
	cs[2].send(protocol.Inbound{Type: "toRoom"})
	if e := cs[2].expect("error", nil); e.Code != "room" {
		t.Fatalf("guest still on the result: %+v", e)
	}
}

func TestFullRoomStartsItself(t *testing.T) {
	cs, _ := openRoom(t, startServer(t, time.Minute), "bob", "carol", "dave")
	seats := bySeat(t, cs)
	seats[0].move(0, 0)
	g := seats[3].expect("game", func(m message) bool { return len(m.Moves) == 1 })
	if len(g.Players) != 4 || g.Turn != "O" || g.Moves[0] != (protocol.Move{0, 0, 0}) {
		t.Fatalf("four-player game: %+v", g.Game)
	}

	// A player who leaves resigns, and the others play on.
	seats[1].send(protocol.Inbound{Type: "leave"})
	g = seats[0].expect("game", func(m message) bool { return m.Players[1].Out != "" })
	if g.Over || g.Turn != "T" || g.Players[1].Out != "resign" {
		t.Fatalf("after O left: %+v", g.Game)
	}
}

func TestRoomMembersComeAndGo(t *testing.T) {
	url := startServer(t, time.Minute)
	cs, code := openRoom(t, url, "bob", "carol")
	host := cs[0]
	host.expect("room", func(m message) bool { return len(m.Players) == 3 })

	cs[1].conn.Close(websocket.StatusNormalClosure, "")
	r := host.expect("room", func(m message) bool { return len(m.Players) == 2 })
	if r.Players[1].Name != "carol" {
		t.Fatalf("after bob dropped: %+v", r.Players)
	}

	host.send(protocol.Inbound{Type: "cancel"})
	host.expect("lobby", nil)
	if e := cs[2].expect("error", nil); e.Code != "room" || !strings.Contains(e.Message, "closed") {
		t.Fatalf("host closed the room: %+v", e)
	}
	cs[2].expect("lobby", nil)

	late := dial(t, url)
	late.hello("")
	late.send(protocol.Inbound{Type: "enter", Code: code, Name: "dave"})
	if e := late.expect("error", nil); e.Code != "room" {
		t.Fatalf("closed room: %+v", e)
	}
}

func TestRoomWaitsForHost(t *testing.T) {
	url := startServer(t, time.Minute)
	host := dial(t, url)
	tok := host.hello("")
	host.send(protocol.Inbound{Type: "room", Name: "host"})
	code := host.expect("room", nil).Code
	guest := dial(t, url)
	guest.hello("")
	guest.send(protocol.Inbound{Type: "enter", Code: code, Name: "bob"})
	guest.expect("room", func(m message) bool { return len(m.Players) == 2 })

	host.conn.Close(websocket.StatusNormalClosure, "")
	guest.expect("room", func(m message) bool { return !m.Players[0].Connected })
	host2 := dial(t, url)
	host2.hello(tok)
	if r := host2.expect("room", nil); r.Code != code || r.You != 0 || len(r.Players) != 2 {
		t.Fatalf("host back: %+v", r)
	}
	guest.expect("room", func(m message) bool { return m.Players[0].Connected })
}
