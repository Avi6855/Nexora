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

	"github.com/nexora/nexora/shared/auth"
)

const httpTimeout = 3 * time.Second

// TransferRiskRequest mirrors fraud-service domain.TransferRiskRequest.
type TransferRiskRequest struct {
	RequestID        string `json:"request_id"`
	UserID           string `json:"user_id"`
	AccountID        string `json:"account_id"`
	Amount           int64  `json:"amount"`
	Currency         string `json:"currency"`
	CounterpartyID   string `json:"counterparty_id,omitempty"`
	CounterpartyName string `json:"counterparty_name,omitempty"`
	Reference        string `json:"reference,omitempty"`
	DeviceID         string `json:"device_id,omitempty"`
	IPAddress        string `json:"ip_address,omitempty"`
}

// TransferRiskResponse mirrors fraud-service domain.TransferRiskResponse.
type TransferRiskResponse struct {
	RequestID  string   `json:"request_id"`
	Action     string   `json:"action"` // ALLOW | REVIEW | STEP_UP | BLOCK
	RiskScore  float64  `json:"risk_score"`
	RiskLevel  string   `json:"risk_level"`
	Reasons    []string `json:"reasons"`
	Advice     string   `json:"advice,omitempty"`
	EvaluatedAt time.Time `json:"evaluated_at"`
}

// FraudClient asks the fraud service for the outbound-transfer scam decision
// before a payment is accepted. It is constructed only when FRAUD_SERVICE_URL
// is configured, so tests and local runs without the fraud service keep
// working (fail-open by omission, never by error).
type FraudClient struct {
	baseURL string
	http    *http.Client
}

// NewFraudClient returns nil when the fraud service is not configured.
func NewFraudClient() *FraudClient {
	baseURL := os.Getenv("FRAUD_SERVICE_URL")
	if baseURL == "" {
		return nil
	}
	return &FraudClient{baseURL: baseURL, http: &http.Client{Timeout: httpTimeout}}
}

// EvaluateTransfer returns the scam-intelligence decision for an outbound
// payment.
func (c *FraudClient) EvaluateTransfer(ctx context.Context, req *TransferRiskRequest) (*TransferRiskResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/fraud/transfer/evaluate", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	auth.AddInternalToken(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("fraud service unreachable: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fraud service returned %d: %s", resp.StatusCode, string(data))
	}
	var decision TransferRiskResponse
	if err := json.Unmarshal(data, &decision); err != nil {
		return nil, err
	}
	return &decision, nil
}
