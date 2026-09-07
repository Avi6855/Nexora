package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/nexora/nexora/shared/auth"
)

const httpTimeout = 3 * time.Second

// LedgerClient reads real account state from the ledger service so insights
// are computed over actual money movement, never caller-supplied snapshots.
type LedgerClient struct {
	baseURL string
	http    *http.Client
}

// NewLedgerClient builds the ledger reader.
func NewLedgerClient() *LedgerClient {
	baseURL := os.Getenv("LEDGER_SERVICE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8084"
	}
	return &LedgerClient{baseURL: baseURL, http: &http.Client{Timeout: httpTimeout}}
}

// BalanceBreakdown mirrors the ledger balance payload.
type BalanceBreakdown struct {
	AccountID    string `json:"account_id"`
	Balance      int64  `json:"balance"`
	Pending      int64  `json:"pending"`
	Available    int64  `json:"available"`
	TotalDebits  int64  `json:"total_debits"`
	TotalCredits int64  `json:"total_credits"`
}

// GetBalance fetches the live balance breakdown for an account.
func (c *LedgerClient) GetBalance(ctx context.Context, accountID uuid.UUID) (*BalanceBreakdown, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/v1/ledger/accounts/"+accountID.String()+"/balance", nil)
	if err != nil {
		return nil, err
	}
	auth.AddInternalToken(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ledger service unreachable: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ledger balance returned %d: %s", resp.StatusCode, string(data))
	}
	var bb BalanceBreakdown
	if err := json.Unmarshal(data, &bb); err != nil {
		return nil, err
	}
	return &bb, nil
}

// Entry is one ledger entry used for the 30-day category aggregation.
type Entry struct {
	EntryID      string    `json:"entry_id"`
	EntryType    string    `json:"entry_type"`
	EntryDirect  string    `json:"entry_direction"`
	Amount       int64     `json:"amount"`
	Category     string    `json:"category"`
	Description  string    `json:"description"`
	BalanceAfter int64     `json:"balance_after"`
	CreatedAt    time.Time `json:"created_at"`
}

// GetEntries reads the account's recent ledger entries (used to aggregate the
// 30-day category spend for safe-to-spend).
func (c *LedgerClient) GetEntries(ctx context.Context, accountID uuid.UUID, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 500
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/v1/ledger/accounts/"+accountID.String()+"/entries?limit="+fmt.Sprint(limit), nil)
	if err != nil {
		return nil, err
	}
	auth.AddInternalToken(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ledger service unreachable: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ledger entries returned %d: %s", resp.StatusCode, string(data))
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}
