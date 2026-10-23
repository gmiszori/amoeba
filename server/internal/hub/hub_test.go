package hub

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/gmiszori/amoeba/server/internal/protocol"
)

// message is the union of every outbound message's fields.
type message struct {
	protocol.Game
	Token   string `json:"token"`
	Name    string `json:"name"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

type testClient struct {
	t    *testing.T
	conn *websocket.Conn
}

func startServer(t *testing.T, turn time.Duration) string {
	t.Helper()
	srv := httptest.NewServer(New(Config{TurnTime: turn}))
	t.Cleanup(srv.Close)
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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		var m message
		if err := wsjson.Read(ctx, c.conn, &m); err != nil {
			c.t.Fatalf("waiting for %q: %v", typ, err)
		}
		if m.Type == typ && (ok == nil || ok(m)) {
			return m
		}
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
	if ga.You == "X" {
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
	o.expect("game", func(m message) bool { return m.Over && m.Rematch.Opponent })
	o.send(protocol.Inbound{Type: "rematch"})

	g := x.expect("game", func(m message) bool { return m.ID != first.ID })
	if g.You != "O" || g.Over {
		t.Fatalf("rematch state for old X: %+v", g.Game)
	}
}

func TestReconnectResumesGame(t *testing.T) {
	url := startServer(t, time.Minute)
	x, o, xToken, _ := matchPair(t, url)
	x.move(0, 0)
	g := o.expect("game", func(m message) bool { return len(m.Moves) == 1 })

	x.conn.Close(websocket.StatusNormalClosure, "")
	o.expect("game", func(m message) bool { return !m.OpponentConnected })

	x2 := dial(t, url)
	if tok := x2.hello(xToken); tok != xToken {
		t.Fatalf("reconnect got a new token")
	}
	resumed := x2.expect("game", nil)
	if resumed.ID != g.ID || resumed.You != "X" || len(resumed.Moves) != 1 {
		t.Fatalf("resumed state: %+v", resumed.Game)
	}
	o.expect("game", func(m message) bool { return m.OpponentConnected })
}

func TestLeaveResigns(t *testing.T) {
	x, o, _, _ := matchPair(t, startServer(t, time.Minute))
	o.send(protocol.Inbound{Type: "leave"})
	o.expect("lobby", nil)
	g := x.expect("game", func(m message) bool { return m.Over })
	if g.Winner != "X" || g.Reason != "resign" || !g.OpponentLeft {
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
