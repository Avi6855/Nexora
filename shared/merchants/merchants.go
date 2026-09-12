// Package merchants implements a merchant identity graph as a tested
// shared library.
//
// Resolution chain: alias → canonical → brand → category → parent. Alias
// observations capture name variants + locations; repeated charges on one
// canonical node surface as subscriptions; risk flags and refund-rate stats
// attach to canonical nodes.
package merchants

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Node is one canonical merchant.
type Node struct {
	ID         string   `json:"id"`
	Canonical  string   `json:"canonical"`
	Brand      string   `json:"brand"`
	Category   string   `json:"category"`
	Parent     string   `json:"parent,omitempty"`
	Aliases    []string `json:"aliases"`
	Locations  []string `json:"locations,omitempty"`
	RiskFlags  []string `json:"risk_flags,omitempty"`
	Charges    int      `json:"charges"`
	TotalMinor int64    `json:"total_minor"`
}

// RefundStats is the refund-rate aggregate for one node.
type RefundStats struct {
	MerchantID    string  `json:"merchant_id"`
	Canonical     string  `json:"canonical"`
	Total         int     `json:"total"`
	Refunded      int     `json:"refunded"`
	RefundRate    float64 `json:"refund_rate"`
	RefundedMinor int64   `json:"refunded_minor"`
}

// Subscription is one detected recurring merchant charge.
type Subscription struct {
	MerchantID string `json:"merchant_id"`
	Canonical  string `json:"canonical"`
	Charges    int    `json:"charges"`
	TotalMinor int64  `json:"total_minor"`
}

var (
	// ErrMerchantNotFound marks unknown merchant IDs.
	ErrMerchantNotFound = errors.New("merchant not found")
)

// subscriptionThreshold is the charge count that marks a subscription.
const subscriptionThreshold = 3

// brandTable seeds brand/category/parent for well-known canonicals.
var brandTable = map[string]struct{ brand, category, parent string }{
	"AMAZON":  {"Amazon", "RETAIL", "AMAZON GROUP"},
	"NETFLIX": {"Netflix", "SUBSCRIPTION", "NETFLIX INC"},
	"TESCO":   {"Tesco", "GROCERY", "TESCO PLC"},
	"UBER":    {"Uber", "TRANSPORT", "UBER TECHNOLOGIES"},
	"SPOTIFY": {"Spotify", "SUBSCRIPTION", "SPOTIFY AB"},
	"SHELL":   {"Shell", "FUEL", "SHELL PLC"},
	"COSTA":   {"Costa", "FOOD_AND_DRINK", "COCA-COLA GROUP"},
	"APPLE":   {"Apple", "RETAIL", "APPLE INC"},
}

// observation is one stored charge.
type observation struct {
	nodeID   string
	amount   int64
	refunded bool
	at       time.Time
}

// Graph is the merchant identity graph.
type Graph struct {
	mu           sync.Mutex
	nodes        map[string]*Node
	byAlias      map[string]string
	observations []observation
}

// NewGraph returns an empty graph.
func NewGraph() *Graph {
	return &Graph{nodes: make(map[string]*Node), byAlias: make(map[string]string)}
}

func normalizeAlias(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// canonicalize maps name variants to one canonical node. Known synonyms
// collapse (AMZN → AMAZON); otherwise the first significant token becomes
// the canonical, so "TESCO STORES 1234" and "Tesco Express" share TESCO.
func canonicalize(name string) string {
	up := strings.ToUpper(name)
	cleaned := strings.Map(func(r rune) rune {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == ' ' {
			return r
		}
		return ' '
	}, up)
	tokens := strings.Fields(cleaned)
	if len(tokens) == 0 {
		return "UNKNOWN"
	}
	stop := map[string]bool{"MKTP": true, "UK": true, "CO": true, "COM": true, "LTD": true, "LIMITED": true, "STORES": true, "STORE": true, "EXPRESS": true, "PRIME": true, "MARKETPLACE": true}
	var sig []string
	for _, tok := range tokens {
		if tok == "AMZN" {
			tok = "AMAZON"
		}
		if stop[tok] {
			continue
		}
		// Pure numbers (store IDs, postcodes) carry no identity.
		isNum := true
		for _, r := range tok {
			if r < '0' || r > '9' {
				isNum = false
			}
		}
		if isNum {
			continue
		}
		sig = append(sig, tok)
	}
	if len(sig) == 0 {
		// All tokens were stop-words/numbers: fall back to the first token
		// mapped through synonyms so "AMZN MKTP" still resolves to AMAZON.
		first := tokens[0]
		if first == "AMZN" {
			return "AMAZON"
		}
		return first
	}
	if sig[0] == "AMZN" {
		return "AMAZON"
	}
	return sig[0]
}

func brandFor(canonical string) (brand, category, parent string) {
	if b, ok := brandTable[canonical]; ok {
		return b.brand, b.category, b.parent
	}
	brand = strings.Title(strings.ToLower(canonical))
	return brand, "GENERAL", ""
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Observe records an alias observation (name variant + location) and its
// charge, creating the canonical node on first sight.
func (g *Graph) Observe(name, location string, amountMinor int64, refunded bool, at time.Time) (*Node, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("merchant name is required")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	canonical := canonicalize(name)
	alias := normalizeAlias(name)
	g.mu.Lock()
	defer g.mu.Unlock()
	id, ok := g.byAlias[alias]
	if !ok {
		// Known canonical under a new variant still resolves to one node.
		if _, exists := g.nodes[canonical]; !exists {
			brand, category, parent := brandFor(canonical)
			g.nodes[canonical] = &Node{ID: canonical, Canonical: canonical, Brand: brand, Category: category, Parent: parent}
		}
		id = canonical
		g.byAlias[alias] = id
		n := g.nodes[id]
		if !containsStr(n.Aliases, alias) {
			n.Aliases = append(n.Aliases, alias)
			sort.Strings(n.Aliases)
		}
	} else {
		// Alias re-observed: keep the original canonical binding.
		canonical = g.nodes[id].Canonical
	}
	n := g.nodes[id]
	if location != "" && !containsStr(n.Locations, location) {
		n.Locations = append(n.Locations, location)
		sort.Strings(n.Locations)
	}
	n.Charges++
	n.TotalMinor += amountMinor
	g.observations = append(g.observations, observation{nodeID: id, amount: amountMinor, refunded: refunded, at: at})
	return copyNode(n), nil
}

// Resolve follows alias → canonical → brand → category → parent.
func (g *Graph) Resolve(name string) (*Node, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("name is required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if id, ok := g.byAlias[normalizeAlias(name)]; ok {
		return copyNode(g.nodes[id]), nil
	}
	canonical := canonicalize(name)
	if n, ok := g.nodes[canonical]; ok {
		return copyNode(n), nil
	}
	return nil, ErrMerchantNotFound
}

// AddAlias links a new name variant to a canonical node.
func (g *Graph) AddAlias(merchantID, alias string) (*Node, error) {
	if strings.TrimSpace(alias) == "" {
		return nil, fmt.Errorf("alias is required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	n, ok := g.nodes[merchantID]
	if !ok {
		// Allow canonical names as well as IDs (they are identical here).
		if n2, ok2 := g.nodes[normalizeAlias(merchantID)]; ok2 {
			n = n2
		} else {
			return nil, ErrMerchantNotFound
		}
	}
	key := normalizeAlias(alias)
	if existing, dup := g.byAlias[key]; dup && existing != n.ID {
		return nil, fmt.Errorf("alias %q already maps to %s", alias, existing)
	}
	g.byAlias[key] = n.ID
	if !containsStr(n.Aliases, key) {
		n.Aliases = append(n.Aliases, key)
		sort.Strings(n.Aliases)
	}
	return copyNode(n), nil
}

// FlagRisk attaches a risk flag to a merchant node.
func (g *Graph) FlagRisk(merchantID, flag string) (*Node, error) {
	if strings.TrimSpace(flag) == "" {
		return nil, fmt.Errorf("flag is required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	n, ok := g.nodes[merchantID]
	if !ok {
		return nil, ErrMerchantNotFound
	}
	if !containsStr(n.RiskFlags, flag) {
		n.RiskFlags = append(n.RiskFlags, flag)
		sort.Strings(n.RiskFlags)
	}
	return copyNode(n), nil
}

// Get returns one node.
func (g *Graph) Get(merchantID string) (*Node, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, ok := g.nodes[merchantID]
	if !ok {
		return nil, ErrMerchantNotFound
	}
	return copyNode(n), nil
}

// RefundStats computes refund-rate stats for one node.
func (g *Graph) RefundStats(merchantID string) (RefundStats, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, ok := g.nodes[merchantID]
	if !ok {
		return RefundStats{}, ErrMerchantNotFound
	}
	var total, refunded int
	var refundedMinor int64
	for _, o := range g.observations {
		if o.nodeID != merchantID {
			continue
		}
		total++
		if o.refunded {
			refunded++
			refundedMinor += o.amount
		}
	}
	var rate float64
	if total > 0 {
		rate = float64(refunded) / float64(total)
	}
	return RefundStats{MerchantID: n.ID, Canonical: n.Canonical, Total: total, Refunded: refunded, RefundRate: rate, RefundedMinor: refundedMinor}, nil
}

// Subscriptions detects recurring merchant charges (nodes at or above the
// charge-count threshold), sorted by charge count descending.
func (g *Graph) Subscriptions() []Subscription {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []Subscription
	for _, n := range g.nodes {
		if n.Charges >= subscriptionThreshold {
			out = append(out, Subscription{MerchantID: n.ID, Canonical: n.Canonical, Charges: n.Charges, TotalMinor: n.TotalMinor})
		}
	}
	if out == nil {
		out = []Subscription{}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Charges == out[j].Charges {
			return out[i].MerchantID < out[j].MerchantID
		}
		return out[i].Charges > out[j].Charges
	})
	return out
}

func copyNode(n *Node) *Node {
	cp := *n
	cp.Aliases = append([]string(nil), n.Aliases...)
	cp.Locations = append([]string(nil), n.Locations...)
	cp.RiskFlags = append([]string(nil), n.RiskFlags...)
	return &cp
}
