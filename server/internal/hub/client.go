package hub

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/gmiszori/amoeba/server/internal/protocol"
)

const (
	// pingInterval keeps idle connections from being dropped by proxies and NATs.
	pingInterval = 25 * time.Second
	writeTimeout = 10 * time.Second
	readLimit    = 1024
	sendBuffer   = 64

	// A client that reads slowly is disconnected once its unsent messages pass
	// maxQueuedBytes, a few of a long game's ~40 KB states. maxTotalQueuedBytes
	// caps all clients together, well below the server's 256 MB memory limit, so
	// many slow connections can't crash it and end every game.
	maxQueuedBytes      = 128 << 10
	maxTotalQueuedBytes = 64 << 20

	// A person sends maybe one message a second; this leaves plenty of room
	// while stopping floods.
	msgRate  = 10 // per second, sustained
	msgBurst = 20
)

// client is one WebSocket connection. It reads messages and hands them to the
// hub; a separate goroutine writes queued outbound messages and pings.
type client struct {
	conn   *websocket.Conn
	out    chan []byte
	cancel context.CancelFunc

	ip      string   // limitKey of the player's address
	session *session // guarded by Hub.mu

	queued atomic.Int64  // bytes waiting in out
	total  *atomic.Int64 // bytes waiting in every client's out (Hub.queuedBytes)
}

// ServeHTTP upgrades the request to a WebSocket and serves it until it closes.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ip := limitKey(clientIP(r))
	if !h.acquireConn(ip) {
		http.Error(w, "too many connections", http.StatusTooManyRequests)
		return
	}
	defer h.releaseConn(ip)

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: h.cfg.OriginPatterns})
	if err != nil {
		return // Accept has already written the error response
	}
	conn.SetReadLimit(readLimit)

	ctx, cancel := context.WithCancel(r.Context())
	c := &client{conn: conn, out: make(chan []byte, sendBuffer), cancel: cancel, ip: ip, total: &h.queuedBytes}
	written := make(chan struct{})
	defer func() {
		cancel()
		<-written
		h.disconnect(c)
		// Nothing writes or queues for c any more, so what's left in out is freed.
		c.total.Add(-c.queued.Swap(0))
		conn.CloseNow()
	}()
	go func() {
		defer close(written)
		c.writeLoop(ctx)
	}()
	// A connection that hasn't said hello by then is closed, so idle sockets
	// can't hold connection slots.
	helloTimer := time.AfterFunc(h.cfg.HelloTimeout, func() {
		h.mu.Lock()
		idle := c.session == nil
		h.mu.Unlock()
		if idle {
			cancel()
		}
	})
	defer helloTimer.Stop()

	limit := limiter{tokens: msgBurst, last: time.Now()}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if !limit.allow(time.Now()) {
			conn.Close(websocket.StatusPolicyViolation, "too many messages")
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
			c.unqueue(len(b))
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
// rather than allowed to block the hub or fill its memory.
func (c *client) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Error("marshal outbound message", "err", err)
		return
	}
	n := int64(len(b))
	queued, total := c.queued.Add(n), c.total.Add(n)
	if queued > maxQueuedBytes || total > maxTotalQueuedBytes {
		c.unqueue(len(b))
		c.cancel()
		return
	}
	select {
	case c.out <- b:
	default:
		c.unqueue(len(b))
		c.cancel()
	}
}

// unqueue releases n bytes that left the queue or never entered it.
func (c *client) unqueue(n int) {
	c.queued.Add(-int64(n))
	c.total.Add(-int64(n))
}

func (c *client) sendError(msg string) {
	c.send(protocol.Error{Type: "error", Message: msg})
}

// closeSoon closes the connection after giving queued messages a moment to
// be written.
func (c *client) closeSoon() {
	time.AfterFunc(time.Second, c.cancel)
}

// limiter is a token bucket allowing msgRate messages per second on average
// with bursts of up to msgBurst.
type limiter struct {
	tokens float64
	last   time.Time
}

func (l *limiter) allow(now time.Time) bool {
	l.tokens = min(msgBurst, l.tokens+now.Sub(l.last).Seconds()*msgRate)
	l.last = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}

// clientIP returns the address of the player's device. The server is only
// reachable through Caddy, which ignores any X-Forwarded-For a client sends
// and sets it to the real peer address, so its first entry can be trusted.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(first)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// limitKey groups an address for the per-IP limits: IPv4 as is, IPv6 by its
// /64, since one device or customer usually gets a whole /64 and could
// otherwise switch addresses at will.
func limitKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	if addr = addr.Unmap(); addr.Is6() {
		prefix, _ := addr.Prefix(64)
		return prefix.String()
	}
	return addr.String()
}
