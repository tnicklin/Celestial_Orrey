package simc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/tnicklin/celestial_orrey/logger"
)

// StatsServer exposes Queue.Stats() as JSON over HTTP. Listens on a single
// localhost address by default; intended for scraping by Prometheus, curl,
// or a small dashboard.
type StatsServer struct {
	addr   string
	queue  Queue
	logger logger.Logger
	srv    *http.Server
}

// StatsServerParams holds dependencies for the stats server.
type StatsServerParams struct {
	Addr   string
	Queue  Queue
	Logger logger.Logger
}

// NewStatsServer constructs a stats server. An empty Addr disables it
// (Start becomes a no-op).
func NewStatsServer(p StatsServerParams) *StatsServer {
	return &StatsServer{addr: p.Addr, queue: p.Queue, logger: p.Logger}
}

// Start launches the HTTP listener in a background goroutine. Returns nil
// immediately if the address is empty.
func (s *StatsServer) Start(_ context.Context) error {
	if s.addr == "" || s.queue == nil {
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.queue.Stats())
	})
	s.srv = &http.Server{
		Addr:              s.addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) && s.logger != nil {
			s.logger.ErrorW("simc stats server", "addr", s.addr, "error", err)
		}
	}()
	if s.logger != nil {
		s.logger.InfoW("simc stats server listening", "addr", s.addr)
	}
	return nil
}

// Stop shuts down the HTTP server with a short timeout.
func (s *StatsServer) Stop() {
	if s.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(ctx)
}
