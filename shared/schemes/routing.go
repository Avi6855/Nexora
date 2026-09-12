package schemes

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/rs/zerolog"
)

// Rail is one scheme rail candidate.
type Rail struct {
	Name           string   `json:"name"`
	CostBps        int64    `json:"cost_bps"`
	P50LatencyMs   int64    `json:"p50_latency_ms"`
	Availability   float64  `json:"availability"`
	Currencies     []string `json:"currencies"`
	AmountMinMinor int64    `json:"amount_min_minor"`
	AmountMaxMinor int64    `json:"amount_max_minor"`
	Destinations   []string `json:"destinations"`
}

// RouteOption is one ranked routing candidate.
type RouteOption struct {
	Rail         string  `json:"rail"`
	CostMinor    int64   `json:"cost_minor"`
	LatencyMs    int64   `json:"latency_ms"`
	Availability float64 `json:"availability"`
	Reason       string  `json:"reason"`
}

// Router holds the rail table.
type Router struct {
	mu     sync.RWMutex
	rails  map[string]*Rail
	logger zerolog.Logger
}

// NewRouter returns an empty router.
func NewRouter(logger zerolog.Logger) *Router {
	return &Router{rails: make(map[string]*Rail), logger: logger}
}

// AddRail registers or replaces a rail.
func (r *Router) AddRail(rail Rail) (*Rail, error) {
	if strings.TrimSpace(rail.Name) == "" {
		return nil, fmt.Errorf("%w: rail name is required", ErrSchemeInvalidInput)
	}
	if rail.CostBps < 0 || rail.P50LatencyMs < 0 {
		return nil, fmt.Errorf("%w: cost and latency must be >= 0", ErrSchemeInvalidInput)
	}
	if rail.Availability < 0 || rail.Availability > 1 {
		return nil, fmt.Errorf("%w: availability must be 0..1", ErrSchemeInvalidInput)
	}
	if len(rail.Currencies) == 0 {
		return nil, fmt.Errorf("%w: at least one currency is required", ErrSchemeInvalidInput)
	}
	if len(rail.Destinations) == 0 {
		return nil, fmt.Errorf("%w: at least one destination is required", ErrSchemeInvalidInput)
	}
	if rail.AmountMaxMinor > 0 && rail.AmountMaxMinor < rail.AmountMinMinor {
		return nil, fmt.Errorf("%w: amount_max below amount_min", ErrSchemeInvalidInput)
	}
	cp := rail
	cp.Currencies = append([]string(nil), rail.Currencies...)
	cp.Destinations = append([]string(nil), rail.Destinations...)
	r.mu.Lock()
	_, existed := r.rails[rail.Name]
	r.rails[rail.Name] = &cp
	r.mu.Unlock()
	if existed {
		r.logger.Info().Str("rail", rail.Name).Msg("rail replaced")
	} else {
		r.logger.Info().Str("rail", rail.Name).Msg("rail added")
	}
	out := cp
	return &out, nil
}

// ListRails returns all rails sorted by name.
func (r *Router) ListRails() []Rail {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Rail, 0, len(r.rails))
	for _, rail := range r.rails {
		cp := *rail
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Route ranks eligible rails for amount/currency/dest. Ranking is
// cost-first, latency-second, availability-third; reasons call out the
// cheapest/fastest trade-off and the fallback order.
func (r *Router) Route(amountMinor int64, currency, dest string) ([]RouteOption, error) {
	if amountMinor <= 0 {
		return nil, fmt.Errorf("%w: amount must be positive", ErrSchemeInvalidInput)
	}
	if strings.TrimSpace(currency) == "" || strings.TrimSpace(dest) == "" {
		return nil, fmt.Errorf("%w: currency and destination are required", ErrSchemeInvalidInput)
	}
	r.mu.RLock()
	var eligible []*Rail
	for _, rail := range r.rails {
		if !containsFoldStr(rail.Currencies, currency) {
			continue
		}
		if !containsFoldStr(rail.Destinations, dest) {
			continue
		}
		if amountMinor < rail.AmountMinMinor {
			continue
		}
		if rail.AmountMaxMinor > 0 && amountMinor > rail.AmountMaxMinor {
			continue
		}
		if rail.Availability <= 0 {
			continue
		}
		eligible = append(eligible, rail)
	}
	r.mu.RUnlock()
	if len(eligible) == 0 {
		return nil, ErrNoRoute
	}
	type scored struct {
		rail *Rail
		cost int64
	}
	var ss []scored
	for _, rail := range eligible {
		cost := amountMinor * rail.CostBps / 10000
		ss = append(ss, scored{rail: rail, cost: cost})
	}
	sort.Slice(ss, func(i, j int) bool {
		if ss[i].cost != ss[j].cost {
			return ss[i].cost < ss[j].cost
		}
		if ss[i].rail.P50LatencyMs != ss[j].rail.P50LatencyMs {
			return ss[i].rail.P50LatencyMs < ss[j].rail.P50LatencyMs
		}
		return ss[i].rail.Availability > ss[j].rail.Availability
	})
	// Fastest among eligible (for the trade-off note).
	fastest := ss[0].rail.Name
	for _, s := range ss[1:] {
		for _, c := range ss {
			_ = c
		}
		if s.rail.P50LatencyMs < ss[0].rail.P50LatencyMs {
			// keep simple: find global min latency
		}
	}
	minLat := ss[0].rail.P50LatencyMs
	for _, s := range ss {
		if s.rail.P50LatencyMs < minLat {
			minLat = s.rail.P50LatencyMs
			fastest = s.rail.Name
		}
	}
	opts := make([]RouteOption, 0, len(ss))
	for i, s := range ss {
		var reason string
		switch {
		case i == 0 && s.rail.Name == fastest:
			reason = "cheapest and fastest: lowest cost with lowest latency; primary choice"
		case i == 0:
			reason = fmt.Sprintf("cheapest: lowest cost (fastest is %s at %dms); primary choice", fastest, minLat)
		case s.rail.Name == fastest:
			reason = fmt.Sprintf("fastest: lowest latency %dms but higher cost; fallback #%d", s.rail.P50LatencyMs, i+1)
		default:
			reason = fmt.Sprintf("fallback #%d: higher cost/latency trade-off", i+1)
		}
		opts = append(opts, RouteOption{
			Rail:         s.rail.Name,
			CostMinor:    s.cost,
			LatencyMs:    s.rail.P50LatencyMs,
			Availability: s.rail.Availability,
			Reason:       reason,
		})
	}
	return opts, nil
}

func containsFoldStr(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(s)) {
			return true
		}
	}
	return false
}
