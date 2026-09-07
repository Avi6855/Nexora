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

// LedgerTransfer is the booking result returned by the ledger service.
type LedgerTransfer struct {
	Transaction struct {
		TransactionID uuid.UUID `json:"transaction_id"`
		Status        string    `json:"status"`
	} `json:"transaction"`
	Entries []struct {
		EntryID      uuid.UUID `json:"entry_id"`
		AccountID    uuid.UUID `json:"account_id"`
		EntryType    string    `json:"entry_type"`
		Amount       int64     `json:"amount"`
		BalanceAfter int64     `json:"balance_after"`
	} `json:"entries"`
}

// ErrInsufficientFunds marks a ledger 402 (availability check failed).
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

// BookTransfer moves money through the ledger with an atomic availability
// check and exactly-once idempotency on the source account.
func (c *LedgerClient) BookTransfer(ctx context.Context, source, dest uuid.UUID, amount int64, currency, idempotencyKey, description string) (*LedgerTransfer, error) {
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
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	auth.AddInternalToken(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ledger service unreachable: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusPaymentRequired {
		return nil, ErrInsufficientFunds
	}
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("ledger transfer returned %d: %s", resp.StatusCode, string(data))
	}
	var result LedgerTransfer
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// AccountInfo is the subset of account-service's account payload used for
// ownership + lockdown checks.
type AccountInfo struct {
	AccountID       uuid.UUID `json:"account_id"`
	UserID          uuid.UUID `json:"user_id"`
	Status          string    `json:"status"`
	LockdownEnabled bool      `json:"lockdown_enabled"`
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

// GetAccount fetches an account by id using the internal service credential.
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
