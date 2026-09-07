package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/nexora/nexora/shared/auth"
)

// AccountClient reads account state from account-service so payment
// creation can refuse payments out of locked accounts before any money
// moves.
type AccountClient struct {
	baseURL string
	http    *http.Client
}

// NewAccountClient builds the client (in-network URL from env).
func NewAccountClient() *AccountClient {
	baseURL := os.Getenv("ACCOUNT_SERVICE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8083"
	}
	return &AccountClient{baseURL: baseURL, http: &http.Client{Timeout: httpTimeout}}
}

// AccountInfo is the subset of the account payload payment-service needs.
type AccountInfo struct {
	AccountID       string `json:"account_id"`
	UserID          string `json:"user_id"`
	Status          string `json:"status"`
	LockdownEnabled bool   `json:"lockdown_enabled"`
}

// GetAccount fetches an account with the internal service credential.
func (c *AccountClient) GetAccount(ctx context.Context, accountID string) (*AccountInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/accounts/"+accountID, nil)
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
