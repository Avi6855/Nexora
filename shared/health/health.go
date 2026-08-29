package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type Status string

const (
	StatusUp   Status = "UP"
	StatusDown Status = "DOWN"
)

type HealthCheck struct {
	Status    Status            `json:"status"`
	Checks    map[string]Status `json:"checks"`
	Timestamp time.Time         `json:"timestamp"`
}

type HealthServer struct {
	checks   map[string]HealthFunc
	mu       sync.RWMutex
	server   *http.Server
}

type HealthFunc func(ctx context.Context) error

func NewHealthServer(addr string) *HealthServer {
	hs := &HealthServer{
		checks: make(map[string]HealthFunc),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", hs.healthHandler)
	mux.HandleFunc("/ready", hs.readyHandler)
	mux.HandleFunc("/live", hs.livenessHandler)

	hs.server = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	return hs
}

func (hs *HealthServer) RegisterCheck(name string, fn HealthFunc) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.checks[name] = fn
}

func (hs *HealthServer) Start() error {
	return hs.server.ListenAndServe()
}

func (hs *HealthServer) Shutdown(ctx context.Context) error {
	return hs.server.Shutdown(ctx)
}

func (hs *HealthServer) healthHandler(w http.ResponseWriter, r *http.Request) {
	hs.mu.RLock()
	defer hs.mu.RUnlock()

	checks := make(map[string]Status)
	allUp := true

	for name, fn := range hs.checks {
		if fn != nil {
			if err := fn(r.Context()); err != nil {
				checks[name] = StatusDown
				allUp = false
			} else {
				checks[name] = StatusUp
			}
		} else {
			checks[name] = StatusUp
		}
	}

	status := StatusUp
	if !allUp {
		status = StatusDown
	}

	health := HealthCheck{
		Status:    status,
		Checks:    checks,
		Timestamp: time.Now().UTC(),
	}

	w.Header().Set("Content-Type", "application/json")
	if !allUp {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(health)
}

func (hs *HealthServer) readyHandler(w http.ResponseWriter, r *http.Request) {
	hs.mu.RLock()
	defer hs.mu.RUnlock()

	checks := make(map[string]Status)
	allReady := true

	for name, fn := range hs.checks {
		if fn != nil {
			if err := fn(r.Context()); err != nil {
				checks[name] = StatusDown
				allReady = false
			} else {
				checks[name] = StatusUp
			}
		} else {
			checks[name] = StatusUp
		}
	}

	status := StatusUp
	if !allReady {
		status = StatusDown
	}

	w.Header().Set("Content-Type", "application/json")
	if !allReady {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(HealthCheck{
		Status:    status,
		Checks:    checks,
		Timestamp: time.Now().UTC(),
	})
}

func (hs *HealthServer) livenessHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(HealthCheck{
		Status:    StatusUp,
		Checks:    map[string]Status{},
		Timestamp: time.Now().UTC(),
	})
}
