package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
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

// ErrIndeterminate marks a booking whose outcome nobody knows: the ledger was
// unreachable, the call timed out, or it answered 5xx. The money may or may not
// have moved, so the caller must not record a definite answer.
var ErrIndeterminate = fmt.Errorf("indeterminate ledger outcome")

// IsIndeterminate reports whether a booking outcome is unknown rather than
// refused. A refused booking is money that did not move; an indeterminate one is
// money that might have.
func IsIndeterminate(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrIndeterminate) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

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
		// The request may have been applied before the connection broke: the
		// ledger of record is the only place that knows.
		return nil, fmt.Errorf("%w: ledger service unreachable: %v", ErrIndeterminate, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusPaymentRequired {
		return nil, ErrInsufficientFunds
	}
	if resp.StatusCode >= http.StatusInternalServerError {
		// A 5xx may be a response written after the booking committed.
		return nil, fmt.Errorf("%w: ledger transfer returned %d: %s", ErrIndeterminate, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("ledger transfer refused with %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
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
