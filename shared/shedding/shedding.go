package shedding

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ThrottleDecision mirrors control-plane-service domain.ThrottleDecision.
type ThrottleDecision struct {
	Service     string `json:"service"`
	ShedPct     int    `json:"shed_pct"`
	Reason      string `json:"reason"`
	TriggeredBy string `json:"triggered_by,omitempty"`
}

// Classifier marks a request's criticality. Critical requests (payments,
// authorisations, ledger reads backing balances) are never shed; non-critical
// ones (analytics, feeds, background polling) are shed first — the banking
// golden path stays alive while the platform degrades.
type Classifier func(r *http.Request) bool

// DefaultClassifier: everything carrying an internal token or touching
// money-movement paths is critical; exploratory/status reads are not.
func DefaultClassifier(r *http.Request) bool {
	if r.Header.Get("X-Internal-Token") != "" {
		return true
	}
	path := r.URL.Path
	for _, prefix := range []string{
		"/v1/payments", "/v1/cards", "/v1/ledger", "/v1/transfers",
		"/v1/accounts", "/v1/pots", "/v1/auth",
	} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// Middleware polls the control plane for the service's shed decision and
// sheds non-critical requests by the instructed percentage.
type Middleware struct {
	service   string
	controlURL string
	classify  Classifier
	client    *http.Client

	mu        sync.RWMutex
	shedPct   int
	reason    string
	counter   uint64

	stop context.CancelFunc
}

// New builds the shedding middleware. controlURL is the control-plane base;
// when empty the middleware stays passive (no shedding).
func New(service, controlURL string, classify Classifier) *Middleware {
	if classify == nil {
		classify = DefaultClassifier
	}
	return &Middleware{
		service:    service,
		controlURL: controlURL,
		classify:   classify,
		client:     &http.Client{Timeout: 2 * time.Second},
	}
}

// Start begins polling the control plane every 10s until ctx is cancelled.
func (m *Middleware) Start(ctx context.Context) {
	if m.controlURL == "" {
		return
	}
	pollCtx, cancel := context.WithCancel(ctx)
	m.stop = cancel
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		m.refresh(pollCtx) // initial fetch
		for {
			select {
			case <-pollCtx.Done():
				return
			case <-t.C:
				m.refresh(pollCtx)
			}
		}
	}()
}

// Stop halts polling.
func (m *Middleware) Stop() {
	if m.stop != nil {
		m.stop()
	}
}

func (m *Middleware) refresh(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		m.controlURL+"/v1/control/throttle/"+m.service, nil)
	if err != nil {
		return
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return // keep last decision; silence never sheds more
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var decision ThrottleDecision
	if err := json.NewDecoder(resp.Body).Decode(&decision); err != nil {
		return
	}
	m.mu.Lock()
	m.shedPct = decision.ShedPct
	m.reason = decision.Reason
	m.mu.Unlock()
}

// Handler wraps next with the shedding decision.
func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		pct := m.shedPct
		m.mu.RUnlock()

		if pct > 0 && !m.classify(r) {
			// Deterministic-ish sampling: shed pct% of non-critical requests.
			m.mu.Lock()
			m.counter++
			n := m.counter
			m.mu.Unlock()
			if n%100 < uint64(pct) {
				w.Header().Set("Retry-After", strconv.Itoa(5))
				w.Header().Set("X-Shed-Reason", m.currentReason())
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":"shed","message":"service is protecting critical paths; retry shortly"}`))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Middleware) currentReason() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.reason == "" {
		return "load shedding active"
	}
	return m.reason
}
