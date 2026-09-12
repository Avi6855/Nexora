package schemes

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// ScenarioKind is one scheme protocol scenario.
type ScenarioKind string

const (
	ScenarioAuth       ScenarioKind = "AUTH"
	ScenarioClearing   ScenarioKind = "CLEARING"
	ScenarioReversal   ScenarioKind = "REVERSAL"
	ScenarioRefund     ScenarioKind = "REFUND"
	ScenarioChargeback ScenarioKind = "CHARGEBACK"
	ScenarioAdvice     ScenarioKind = "ADVICE"
)

// AllScenarioKinds lists the certification suite.
var AllScenarioKinds = []ScenarioKind{
	ScenarioAuth, ScenarioClearing, ScenarioReversal,
	ScenarioRefund, ScenarioChargeback, ScenarioAdvice,
}

// CertCase is one canned network message with its expected response.
type CertCase struct {
	ID       string            `json:"id"`
	Kind     ScenarioKind      `json:"kind"`
	Message  map[string]string `json:"message"`
	Expected map[string]string `json:"expected"`
}

// CaseResult is the per-case validation outcome.
type CaseResult struct {
	CaseID   string            `json:"case_id"`
	Kind     ScenarioKind      `json:"kind"`
	Pass     bool              `json:"pass"`
	Reason   string            `json:"reason"`
	Actual   map[string]string `json:"actual"`
	Expected map[string]string `json:"expected"`
}

// CertReport is one full suite run.
type CertReport struct {
	RunID     string       `json:"run_id"`
	At        time.Time    `json:"at"`
	Results   []CaseResult `json:"results"`
	Passed    int          `json:"passed"`
	Failed    int          `json:"failed"`
	PassRate  float64      `json:"pass_rate"`
	AllPassed bool         `json:"all_passed"`
}

// CertHarness holds the scenario suite and past runs.
type CertHarness struct {
	mu     sync.RWMutex
	cases  map[string]*CertCase
	order  []string
	runs   map[string]*CertReport
	logger zerolog.Logger
}

// NewCertHarness returns an empty harness.
func NewCertHarness(logger zerolog.Logger) *CertHarness {
	return &CertHarness{cases: make(map[string]*CertCase), runs: make(map[string]*CertReport), logger: logger}
}

func validKind(k ScenarioKind) bool {
	for _, a := range AllScenarioKinds {
		if a == k {
			return true
		}
	}
	return false
}

func copyMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// RegisterCase adds one canned scenario to the suite.
func (h *CertHarness) RegisterCase(kind ScenarioKind, message, expected map[string]string) (*CertCase, error) {
	if !validKind(kind) {
		return nil, fmt.Errorf("%w: unknown scenario %q", ErrSchemeInvalidInput, kind)
	}
	if len(message) == 0 {
		return nil, fmt.Errorf("%w: message is required", ErrSchemeInvalidInput)
	}
	if len(expected) == 0 {
		return nil, fmt.Errorf("%w: expected response is required", ErrSchemeInvalidInput)
	}
	c := &CertCase{ID: "cert-" + uuid.NewString(), Kind: kind, Message: copyMap(message), Expected: copyMap(expected)}
	h.mu.Lock()
	h.cases[c.ID] = c
	h.order = append(h.order, c.ID)
	h.mu.Unlock()
	h.logger.Info().Str("case_id", c.ID).Str("kind", string(kind)).Msg("cert case registered")
	return &CertCase{ID: c.ID, Kind: c.Kind, Message: copyMap(c.Message), Expected: copyMap(c.Expected)}, nil
}

// ListCases returns the suite in registration order.
func (h *CertHarness) ListCases() []*CertCase {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*CertCase, 0, len(h.order))
	for _, id := range h.order {
		c := h.cases[id]
		out = append(out, &CertCase{ID: c.ID, Kind: c.Kind, Message: copyMap(c.Message), Expected: copyMap(c.Expected)})
	}
	return out
}

// RunAll validates every case against the simulated protocol and stores
// the certification report.
func (h *CertHarness) RunAll(now time.Time) (*CertReport, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.order) == 0 {
		return nil, fmt.Errorf("%w: no certification cases registered", ErrSchemeInvalidInput)
	}
	rep := &CertReport{RunID: "run-" + uuid.NewString(), At: now}
	for _, id := range h.order {
		c := h.cases[id]
		actual := simulateResponse(c.Kind, c.Message)
		pass, reason := compareResponses(c.Expected, actual)
		rep.Results = append(rep.Results, CaseResult{
			CaseID: c.ID, Kind: c.Kind, Pass: pass, Reason: reason,
			Actual: copyMap(actual), Expected: copyMap(c.Expected),
		})
		if pass {
			rep.Passed++
		} else {
			rep.Failed++
		}
	}
	sort.Slice(rep.Results, func(i, j int) bool { return rep.Results[i].CaseID < rep.Results[j].CaseID })
	total := len(rep.Results)
	if total > 0 {
		rep.PassRate = float64(rep.Passed) / float64(total)
	}
	rep.AllPassed = rep.Failed == 0
	h.runs[rep.RunID] = rep
	h.logger.Info().Str("run_id", rep.RunID).Int("passed", rep.Passed).Int("failed", rep.Failed).Msg("cert run completed")
	cp := *rep
	cp.Results = append([]CaseResult(nil), rep.Results...)
	return &cp, nil
}

// GetRun fetches one stored report.
func (h *CertHarness) GetRun(id string) (*CertReport, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	r, ok := h.runs[id]
	if !ok {
		return nil, ErrCertRunNotFound
	}
	cp := *r
	cp.Results = append([]CaseResult(nil), r.Results...)
	return &cp, nil
}

func compareResponses(expected, actual map[string]string) (bool, string) {
	var diffs []string
	for k, want := range expected {
		got, ok := actual[k]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("missing field %q (want %q)", k, want))
			continue
		}
		if got != want {
			diffs = append(diffs, fmt.Sprintf("field %q: want %q got %q", k, want, got))
		}
	}
	if len(diffs) > 0 {
		return false, "FAIL: " + strings.Join(diffs, "; ")
	}
	return true, "PASS: response matches expected"
}

// simulateResponse is the deterministic scheme protocol stub: canned
// network messages map to protocol responses per scenario kind.
func simulateResponse(kind ScenarioKind, msg map[string]string) map[string]string {
	get := func(k string) string { return strings.TrimSpace(msg[k]) }
	switch kind {
	case ScenarioAuth:
		if get("amount") == "" || get("currency") == "" || get("pan") == "" {
			return map[string]string{"response_code": "30", "meaning": "format error: amount/currency/pan required"}
		}
		if get("amount") == "0" {
			return map[string]string{"response_code": "05", "meaning": "do not honour"}
		}
		return map[string]string{"response_code": "00", "meaning": "approved", "auth_code": "AUTH" + get("amount")}
	case ScenarioClearing:
		if get("auth_code") == "" || get("amount") == "" {
			return map[string]string{"clearing_status": "REJECTED", "reason": "auth_code/amount required"}
		}
		return map[string]string{"clearing_status": "CLEARED", "batch": "B-" + get("auth_code")}
	case ScenarioReversal:
		if get("original_auth") == "" {
			return map[string]string{"reversal_status": "REJECTED", "reason": "original_auth required"}
		}
		return map[string]string{"reversal_status": "REVERSED", "original_auth": get("original_auth")}
	case ScenarioRefund:
		if get("original_tx") == "" || get("amount") == "" {
			return map[string]string{"refund_status": "REJECTED", "reason": "original_tx/amount required"}
		}
		return map[string]string{"refund_status": "REFUNDED", "original_tx": get("original_tx")}
	case ScenarioChargeback:
		if get("original_tx") == "" || get("reason") == "" {
			return map[string]string{"chargeback_status": "REJECTED", "reason": "original_tx/reason required"}
		}
		return map[string]string{"chargeback_status": "ACCEPTED", "original_tx": get("original_tx")}
	case ScenarioAdvice:
		if get("advice_code") == "" {
			return map[string]string{"advice_status": "REJECTED", "reason": "advice_code required"}
		}
		return map[string]string{"advice_status": "RECORDED", "advice_code": get("advice_code")}
	default:
		return map[string]string{"status": "UNKNOWN_KIND"}
	}
}
