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
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/shared/auth"
)

// ErrInsufficientFunds marks a hold the account cannot cover. The saga treats
// it as a refusal, not an outage: the payment is not authorised.
var ErrInsufficientFunds = errors.New("insufficient funds")

// LedgerReservation mirrors the ledger-service Reservation response.
type LedgerReservation struct {
	ReservationID uuid.UUID `json:"reservation_id"`
	Status        string    `json:"status"`
	Amount        int64     `json:"amount"`
	Currency      string    `json:"currency"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// LedgerClient talks to the ledger of record. Payment authorisation reserves
// funds here so the money cannot be spent twice while the payment is in flight.
type LedgerClient struct {
	baseURL string
	http    *http.Client
	logger  zerolog.Logger
}

// DefaultLedgerURL resolves the ledger endpoint: docker-compose passes the
// in-network name, local runs fall back to localhost.
func DefaultLedgerURL() string {
	if u := os.Getenv("LEDGER_SERVICE_URL"); u != "" {
		return u
	}
	return "http://localhost:8084"
}

func NewLedgerClient(logger zerolog.Logger) *LedgerClient {
	return &LedgerClient{
		baseURL: DefaultLedgerURL(),
		http:    &http.Client{Timeout: httpTimeout},
		logger:  logger,
	}
}

// Reserve places a hold for amount on the account. txID ties the hold to the
// payment so the ledger entry it eventually produces is traceable back to it.
func (c *LedgerClient) Reserve(ctx context.Context, accountID uuid.UUID, amount int64, currency string, txID uuid.UUID, ttl string) (*LedgerReservation, error) {
	req := map[string]interface{}{
		"account_id":     accountID.String(),
		"amount":         amount,
		"currency":       currency,
		"transaction_id": txID.String(),
		"ttl":            ttl,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/ledger/reserve", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	auth.AddInternalToken(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ledger service unreachable: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusConflict {
		return nil, fmt.Errorf("%w: %s", ErrInsufficientFunds, strings.TrimSpace(string(data)))
	}
	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("account locked: %s", strings.TrimSpace(string(data)))
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ledger reserve returned %d: %s", resp.StatusCode, string(data))
	}

	var reservation LedgerReservation
	if err := json.Unmarshal(data, &reservation); err != nil {
		return nil, fmt.Errorf("decoding reservation: %w", err)
	}
	if reservation.ReservationID == uuid.Nil {
		return nil, fmt.Errorf("ledger reserve returned no reservation id: %s", string(data))
	}
	return &reservation, nil
}

// ReleaseReservation frees a hold. Releasing a hold that is already gone is
// reported as an error so the caller can log it, but it is never fatal: the
// funds are not held any more either way.
func (c *LedgerClient) ReleaseReservation(ctx context.Context, reservationID uuid.UUID) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/ledger/reservations/"+reservationID.String()+"/release", nil)
	if err != nil {
		return err
	}
	auth.AddInternalToken(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("ledger service unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ledger release returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}

// IsInsufficientFunds reports whether err is a refusal rather than an outage.
func IsInsufficientFunds(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrInsufficientFunds) || strings.Contains(strings.ToLower(err.Error()), "insufficient funds")
}
