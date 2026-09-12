package schemes

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// FeeBreakdown is one transaction's fee components (minor units).
type FeeBreakdown struct {
	TxID        string    `json:"tx_id"`
	Scheme      string    `json:"scheme"`
	Currency    string    `json:"currency"`
	Network     int64     `json:"network_minor"`
	Processor   int64     `json:"processor_minor"`
	FX          int64     `json:"fx_minor"`
	Interchange int64     `json:"interchange_minor"`
	Internal    int64     `json:"internal_minor"`
	At          time.Time `json:"at"`
}

// FeeTotal is the attributed total.
type FeeTotal struct {
	TxID       string       `json:"tx_id"`
	Scheme     string       `json:"scheme"`
	Currency   string       `json:"currency"`
	TotalMinor int64        `json:"total_minor"`
	Breakdown  FeeBreakdown `json:"breakdown"`
}

// FeeSummary aggregates unit economics per scheme.
type FeeSummary struct {
	Scheme      string           `json:"scheme"`
	Count       int              `json:"count"`
	TotalMinor  int64            `json:"total_minor"`
	AvgMinor    float64          `json:"avg_minor"`
	ByComponent map[string]int64 `json:"by_component"`
}

// FeeLedger stores attributed fees.
type FeeLedger struct {
	mu      sync.RWMutex
	entries []FeeBreakdown
	byTx    map[string]FeeBreakdown
	logger  zerolog.Logger
}

// NewFeeLedger returns an empty ledger.
func NewFeeLedger(logger zerolog.Logger) *FeeLedger {
	return &FeeLedger{byTx: make(map[string]FeeBreakdown), logger: logger}
}

// Attribute validates, totals and stores one breakdown.
func (l *FeeLedger) Attribute(b FeeBreakdown, now time.Time) (*FeeTotal, error) {
	if strings.TrimSpace(b.TxID) == "" {
		return nil, fmt.Errorf("%w: tx_id is required", ErrFeeInvalid)
	}
	if strings.TrimSpace(b.Scheme) == "" {
		return nil, fmt.Errorf("%w: scheme is required", ErrFeeInvalid)
	}
	if strings.TrimSpace(b.Currency) == "" {
		return nil, fmt.Errorf("%w: currency is required", ErrFeeInvalid)
	}
	for name, v := range map[string]int64{
		"network": b.Network, "processor": b.Processor, "fx": b.FX,
		"interchange": b.Interchange, "internal": b.Internal,
	} {
		if v < 0 {
			return nil, fmt.Errorf("%w: %s must be >= 0", ErrFeeInvalid, name)
		}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if b.At.IsZero() {
		b.At = now
	}
	b.Scheme = strings.ToUpper(strings.TrimSpace(b.Scheme))
	total := b.Network + b.Processor + b.FX + b.Interchange + b.Internal
	l.mu.Lock()
	l.entries = append(l.entries, b)
	l.byTx[b.TxID] = b
	l.mu.Unlock()
	l.logger.Info().Str("tx_id", b.TxID).Str("scheme", b.Scheme).Int64("total_minor", total).Msg("fee attributed")
	return &FeeTotal{TxID: b.TxID, Scheme: b.Scheme, Currency: b.Currency, TotalMinor: total, Breakdown: b}, nil
}

// Summary aggregates one scheme.
func (l *FeeLedger) Summary(scheme string) FeeSummary {
	key := strings.ToUpper(strings.TrimSpace(scheme))
	l.mu.RLock()
	defer l.mu.RUnlock()
	sum := FeeSummary{Scheme: key, ByComponent: map[string]int64{
		"network": 0, "processor": 0, "fx": 0, "interchange": 0, "internal": 0,
	}}
	for _, e := range l.entries {
		if e.Scheme != key {
			continue
		}
		sum.Count++
		sum.TotalMinor += e.Network + e.Processor + e.FX + e.Interchange + e.Internal
		sum.ByComponent["network"] += e.Network
		sum.ByComponent["processor"] += e.Processor
		sum.ByComponent["fx"] += e.FX
		sum.ByComponent["interchange"] += e.Interchange
		sum.ByComponent["internal"] += e.Internal
	}
	if sum.Count > 0 {
		sum.AvgMinor = float64(sum.TotalMinor) / float64(sum.Count)
	}
	return sum
}

// SummaryAll aggregates every scheme sorted by scheme name.
func (l *FeeLedger) SummaryAll() []FeeSummary {
	l.mu.RLock()
	schemes := map[string]bool{}
	for _, e := range l.entries {
		schemes[e.Scheme] = true
	}
	l.mu.RUnlock()
	var out []FeeSummary
	for s := range schemes {
		out = append(out, l.Summary(s))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Scheme < out[j].Scheme })
	if out == nil {
		out = []FeeSummary{}
	}
	return out
}
