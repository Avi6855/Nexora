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

// LedgerClient reads the real append-only ledger so replay can reconstruct
// historical account state from actual money movement.
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

// Entry is one append-only ledger row (balance_after is the authoritative
// post-booking balance, which is what makes time-travel exact).
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

// GetEntries reads the account's most recent ledger entries (newest first).
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
