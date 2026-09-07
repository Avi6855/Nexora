package clients

import (
	"bytes"
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

// ErrInsufficientFunds surfaces the ledger's availability decision.
var ErrInsufficientFunds = fmt.Errorf("insufficient funds")

type LedgerClient struct {
	baseURL string
	http    *http.Client
}

func NewLedgerClient() *LedgerClient {
	baseURL := os.Getenv("LEDGER_SERVICE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8084"
	}
	return &LedgerClient{baseURL: baseURL, http: &http.Client{Timeout: httpTimeout}}
}

// BookTransfer books a balanced double-entry movement between the funding
// account and the pot's own ledger account (the pot_id acts as its ledger
// account id, so pot money is auditable and pot balances can be re-derived).
func (c *LedgerClient) BookTransfer(ctx context.Context, source, dest uuid.UUID, amount int64, currency, idempotencyKey, description string) error {
	body, _ := json.Marshal(map[string]interface{}{
		"source_account_id":      source.String(),
		"destination_account_id": dest.String(),
		"amount":                 amount,
		"currency":               currency,
		"idempotency_key":        idempotencyKey,
		"description":            description,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/ledger/transfers", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	auth.AddInternalToken(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("ledger service unreachable: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusPaymentRequired {
		return ErrInsufficientFunds
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("ledger transfer returned %d: %s", resp.StatusCode, string(data))
	}
	return nil
}

type AccountInfo struct {
	AccountID uuid.UUID `json:"account_id"`
	UserID    uuid.UUID `json:"user_id"`
	Status    string    `json:"status"`
}

type AccountClient struct {
	baseURL string
	http    *http.Client
}

func NewAccountClient() *AccountClient {
	baseURL := os.Getenv("ACCOUNT_SERVICE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8083"
	}
	return &AccountClient{baseURL: baseURL, http: &http.Client{Timeout: httpTimeout}}
}

func (c *AccountClient) GetAccount(ctx context.Context, accountID uuid.UUID) (*AccountInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/accounts/"+accountID.String(), nil)
	if err != nil {
		return nil, err
	}
	auth.AddInternalToken(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("account service unreachable: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("account lookup returned %d: %s", resp.StatusCode, string(data))
	}
	var info AccountInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}
