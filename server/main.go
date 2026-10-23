// Command amoeba-server runs the matchmaking and game server.
//
// Configuration comes from the environment:
//
//	PORT             listen port (default 8080)
//	TURN_SECONDS     seconds per turn before the player loses (default 30)
//	ALLOWED_ORIGINS  comma-separated extra WebSocket origin host patterns
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gmiszori/amoeba/server/internal/hub"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "check that a server on PORT is healthy and exit")
	flag.Parse()

	port := env("PORT", "8080")
	if *healthcheck {
		os.Exit(checkHealth(port))
	}

	turnSeconds, err := strconv.Atoi(env("TURN_SECONDS", "30"))
	if err != nil || turnSeconds <= 0 {
		slog.Error("TURN_SECONDS must be a positive integer")
		os.Exit(1)
	}
	var origins []string
	for o := range strings.SplitSeq(os.Getenv("ALLOWED_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	h := hub.New(hub.Config{TurnTime: time.Duration(turnSeconds) * time.Second, OriginPatterns: origins})
	go h.Run(ctx)

	mux := http.NewServeMux()
	mux.Handle("GET /ws", h)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	srv := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	slog.Info("listening", "port", port, "turnSeconds", turnSeconds)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server failed", "err", err)
		os.Exit(1)
	}
}

func checkHealth(port string) int {
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
