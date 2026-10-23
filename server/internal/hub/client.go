package hub

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/gmiszori/amoeba/server/internal/protocol"
)

const (
	// pingInterval stays well below Cloudflare's ~100s idle timeout.
	pingInterval = 25 * time.Second
	writeTimeout = 10 * time.Second
	readLimit    = 1024
	sendBuffer   = 64
)

// client is one WebSocket connection. It reads messages and hands them to the
// hub; a separate goroutine writes queued outbound messages and pings.
type client struct {
	conn   *websocket.Conn
	out    chan []byte
	cancel context.CancelFunc

	session *session // guarded by Hub.mu
}

// ServeHTTP upgrades the request to a WebSocket and serves it until it closes.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: h.cfg.OriginPatterns})
	if err != nil {
		return // Accept has already written the error response
	}
	conn.SetReadLimit(readLimit)

	ctx, cancel := context.WithCancel(r.Context())
	c := &client{conn: conn, out: make(chan []byte, sendBuffer), cancel: cancel}
	defer func() {
		cancel()
		h.disconnect(c)
		conn.CloseNow()
	}()
	go c.writeLoop(ctx)

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg protocol.Inbound
		if err := json.Unmarshal(data, &msg); err != nil {
			c.sendError("malformed message")
			continue
		}
		h.handle(c, msg)
	}
}

func (c *client) writeLoop(ctx context.Context) {
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		var err error
		select {
		case <-ctx.Done():
			return
		case b := <-c.out:
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err = c.conn.Write(wctx, websocket.MessageText, b)
			cancel()
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err = c.conn.Ping(pctx)
			cancel()
		}
		if err != nil {
			c.cancel()
			return
		}
	}
}

// send queues v for writing. A client that can't keep up is disconnected
// rather than allowed to block the hub.
func (c *client) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Error("marshal outbound message", "err", err)
		return
	}
	select {
	case c.out <- b:
	default:
		c.cancel()
	}
}

func (c *client) sendError(msg string) {
	c.send(protocol.Error{Type: "error", Message: msg})
}

// closeSoon closes the connection after giving queued messages a moment to
// be written.
func (c *client) closeSoon() {
	time.AfterFunc(time.Second, c.cancel)
}
