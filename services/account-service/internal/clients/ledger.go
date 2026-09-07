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
	"github.com/rs/zerolog"
)

const httpTimeout = 2 * time.Second

// LedgerBalance is the live balance breakdown the ledger computed from real
// double-entry rows — the single source of truth for what a user can spend.
type LedgerBalance struct {
	AccountID uuid.UUID `json:"account_id"`
	Balance   int64     `json:"balance"`   // cleared funds (sum of settled entries)
	Pending   int64     `json:"pending"`   // sum of ACTIVE fund reservations
	Available int64     `json:"available"` // balance - pending
}

// LedgerClient reads money positions straight from the ledger service.
// The account service deliberately does NOT keep its own balance counter —
// a stale counter is how banking apps show the wrong balance after a card
// payment clears.
type LedgerClient struct {
	baseURL string
	http    *http.Client
	logger  zerolog.Logger
}

func NewLedgerClient(logger zerolog.Logger) *LedgerClient {
	baseURL := os.Getenv("LEDGER_SERVICE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8084"
	}
	return &LedgerClient{baseURL: baseURL, http: &http.Client{Timeout: httpTimeout}, logger: logger}
}

func (c *LedgerClient) Balance(ctx context.Context, accountID uuid.UUID) (*LedgerBalance, error) {
	url := c.baseURL + "/v1/ledger/accounts/" + accountID.String() + "/balance"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
	var b LedgerBalance
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	return &b, nil
}
