package domain

import (
	"encoding/json"
	"time"
)

// FeedItem is the Monzo-style mobile home-screen row. Every row is derived
// from a real persisted notification; amount/merchant/balance enrichments come
// from the event metadata that was stored with it — nothing is synthesized at
// read time.
type FeedItem struct {
	FeedItemID   string    `json:"feed_item_id"`
	Type         string    `json:"type"` // TRANSACTION | SECURITY | FRAUD | ...
	Title        string    `json:"title"`
	Subtitle     string    `json:"subtitle"`
	AmountMinor  *int64    `json:"amount,omitempty"` // signed minor units
	Currency     string    `json:"currency,omitempty"`
	Merchant     string    `json:"merchant,omitempty"`
	BalanceAfter *int64    `json:"balance_after,omitempty"`
	LatencyMs    *int64    `json:"latency_ms,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

func FeedItemFromNotification(n *Notification) *FeedItem {
	item := &FeedItem{
		FeedItemID: n.NotificationID.String(),
		Type:       string(n.NotificationType),
		Title:      n.Title,
		Subtitle:   n.Body,
		CreatedAt:  n.CreatedAt,
	}

	var meta map[string]interface{}
	if len(n.Metadata) > 0 {
		_ = json.Unmarshal(n.Metadata, &meta)
	}

	if amount, ok := num(meta, "amount"); ok {
		signed := amount
		item.AmountMinor = &signed
	}
	if cur, ok := meta["currency"].(string); ok {
		item.Currency = cur
	}
	if merchant, ok := meta["merchant"].(string); ok {
		item.Merchant = merchant
	}
	if bal, ok := num(meta, "balance_after"); ok {
		item.BalanceAfter = &bal
	}
	if lat, ok := num(meta, "latency_ms"); ok {
		item.LatencyMs = &lat
	}
	return item
}

func num(meta map[string]interface{}, key string) (int64, bool) {
	v, ok := meta[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	}
	return 0, false
}
