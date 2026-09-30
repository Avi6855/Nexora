package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nexora/nexora/shared/auth"
)

const httpTimeout = 5 * time.Second

// Resolution outcomes payment-service accepts for a payment that went UNKNOWN
// (ADR-007). Listed here as well as in payment-service so reconciliation can
// name the outcome it decided without importing another service's internals.
const (
	ResolutionOutcomeConfirmed = "CONFIRMED"
	ResolutionOutcomeFailed    = "FAILED"
)

// PaymentClient is reconciliation's view of payment-service: it reads the
// authoritative state of a payment and writes back the outcome reconciliation
// established. Both calls carry the service-to-service credential, because the
// write-back is internal-only — a caller who could declare a payment confirmed
// could book money that never moved.
type PaymentClient struct {
	baseURL string
	http    *http.Client
}

// NewPaymentClient builds the client from the in-network URL.
func NewPaymentClient() *PaymentClient {
	baseURL := strings.TrimRight(os.Getenv("PAYMENT_SERVICE_URL"), "/")
	if baseURL == "" {
		baseURL = "http://localhost:8085"
	}
	return &PaymentClient{baseURL: baseURL, http: &http.Client{Timeout: httpTimeout}}
}

// PaymentSnapshot is the subset of a payment the sweep reasons about.
type PaymentSnapshot struct {
	PaymentID     string `json:"payment_id"`
	State         string `json:"state"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	ReservationID string `json:"reservation_id,omitempty"`
}

// GetPayment reads a payment's current state.
func (c *PaymentClient) GetPayment(ctx context.Context, paymentID string) (*PaymentSnapshot, error) {
	if paymentID == "" {
		return nil, fmt.Errorf("payment id is required")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/payments/"+paymentID, nil)
	if err != nil {
		return nil, err
	}
	auth.AddInternalToken(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("payment service unreachable: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("payment lookup returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var snapshot PaymentSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return nil, fmt.Errorf("decoding payment: %w", err)
	}
	return &snapshot, nil
}

// ResolveUnknown concludes an UNKNOWN payment with the outcome reconciliation
// reached. It is idempotent on the payment side, so a retried write-back after a
// timeout is safe.
func (c *PaymentClient) ResolveUnknown(ctx context.Context, paymentID, outcome, reason string) error {
	if paymentID == "" {
		return fmt.Errorf("payment id is required")
	}
	if outcome != ResolutionOutcomeConfirmed && outcome != ResolutionOutcomeFailed {
		return fmt.Errorf("outcome must be %s or %s, got %q", ResolutionOutcomeConfirmed, ResolutionOutcomeFailed, outcome)
	}

	body, err := json.Marshal(map[string]string{
		"outcome":     outcome,
		"reason":      reason,
		"resolved_by": "reconciliation-service",
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/payments/"+paymentID+"/resolve-unknown", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	auth.AddInternalToken(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("payment service unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("resolving payment returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}
