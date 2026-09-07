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

	"github.com/google/uuid"
	"github.com/nexora/nexora/shared/auth"
	"github.com/rs/zerolog"
)

const httpTimeout = 2 * time.Second

// FraudDecision is the subset of the fraud response the card platform acts on.
type FraudDecision struct {
	Decision string `json:"decision"` // APPROVE | REVIEW | DECLINE
	RiskScore float64 `json:"risk_score"`
	RiskLevel string  `json:"risk_level"`
	Reasons  []string `json:"reasons"`
	Signals  []json.RawMessage `json:"signals"`
}

type FraudClient struct {
	baseURL string
	http    *http.Client
	logger  zerolog.Logger
}

func NewFraudClient(baseURL string, logger zerolog.Logger) *FraudClient {
	if baseURL == "" {
		baseURL = "http://localhost:8089"
	}
	return &FraudClient{baseURL: baseURL, http: &http.Client{Timeout: httpTimeout}, logger: logger}
}

// EvaluateAuthorizationRequest is the live authorisation context sent to the
// fraud service (card row limits + real spend today included).
type EvaluateAuthorizationRequest struct {
	AuthorizationID  string  `json:"authorization_id"`
	UserID           string  `json:"user_id"`
	AccountID        string  `json:"account_id"`
	CardID           string  `json:"card_id"`
	Amount           int64   `json:"amount"`
	Currency         string  `json:"currency"`
	Merchant         string  `json:"merchant"`
	MerchantCategory string  `json:"merchant_category"`
	MerchantCity     string  `json:"merchant_city"`
	MerchantCountry  string  `json:"merchant_country"`
	Latitude         float64 `json:"latitude"`
	Longitude        float64 `json:"longitude"`
	TerminalID       string  `json:"terminal_id"`
	DeviceID         string  `json:"device_id"`
	DailyLimit       int64   `json:"daily_limit"`
	MonthlyLimit     int64   `json:"monthly_limit"`
	SpentToday       int64   `json:"spent_today"`
}

func (c *FraudClient) EvaluateAuthorization(ctx context.Context, req *EvaluateAuthorizationRequest) (*FraudDecision, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/fraud/authorization/evaluate", bytes.NewReader(body))
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
	var decision FraudDecision
	if err := json.Unmarshal(data, &decision); err != nil {
		return nil, err
	}
	return &decision, nil
}

// AccountClient reads account state from account-service (lockdown
// enforcement for card payments).
type AccountClient struct {
	baseURL string
	http    *http.Client
}

// NewAccountClient builds the client.
func NewAccountClient() *AccountClient {
	baseURL := os.Getenv("ACCOUNT_SERVICE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8083"
	}
	return &AccountClient{baseURL: baseURL, http: &http.Client{Timeout: httpTimeout}}
}

// AccountLockdown mirrors the account payload's lockdown flag.
type AccountLockdown struct {
	AccountID       string `json:"account_id"`
	LockdownEnabled bool   `json:"lockdown_enabled"`
	UserID          string `json:"user_id"`
}

// GetLockdown returns the account's emergency-lockdown state. Caller decides
// the failure policy (fail-open here: error -> not locked, the ledger
// availability check still guards the money).
func (c *AccountClient) GetLockdown(ctx context.Context, accountID uuid.UUID) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/accounts/"+accountID.String(), nil)
	if err != nil {
		return false, err
	}
	auth.AddInternalToken(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("account service unreachable: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("account lookup returned %d: %s", resp.StatusCode, string(data))
	}
	var info AccountLockdown
	if err := json.Unmarshal(data, &info); err != nil {
		return false, err
	}
	return info.LockdownEnabled, nil
}

// LedgerReservation mirrors the ledger-service Reservation response.
type LedgerReservation struct {
	ReservationID uuid.UUID `json:"reservation_id"`
	Status        string    `json:"status"`
	Amount        int64     `json:"amount"`
	Currency      string    `json:"currency"`
}

// LedgerEntry mirrors a settled ledger entry (carries the real balance_after).
type LedgerEntry struct {
	EntryID       uuid.UUID `json:"entry_id"`
	EntryType     string    `json:"entry_type"`
	Amount        int64     `json:"amount"`
	BalanceAfter  int64     `json:"balance_after"`
	CreatedAt     time.Time `json:"created_at"`
}

type LedgerClient struct {
	baseURL string
	http    *http.Client
	logger  zerolog.Logger
}

func NewLedgerClient(baseURL string, logger zerolog.Logger) *LedgerClient {
	if baseURL == "" {
		baseURL = "http://localhost:8084"
	}
	return &LedgerClient{baseURL: baseURL, http: &http.Client{Timeout: httpTimeout}, logger: logger}
}

type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// DefaultServiceURLs resolve inter-service endpoints: docker-compose passes
// the in-network names; local runs fall back to localhost.
func DefaultFraudURL() string {
	if u := os.Getenv("FRAUD_SERVICE_URL"); u != "" {
		return u
	}
	return "http://localhost:8089"
}

func DefaultLedgerURL() string {
	if u := os.Getenv("LEDGER_SERVICE_URL"); u != "" {
		return u
	}
	return "http://localhost:8084"
}

func (c *LedgerClient) Reserve(ctx context.Context, accountID uuid.UUID, amount int64, currency string, txID uuid.UUID, ttl string) (*LedgerReservation, error) {
	req := map[string]interface{}{
		"account_id":     accountID.String(),
		"amount":         amount,
		"currency":       currency,
		"transaction_id": txID.String(),
		"ttl":            ttl,
	}
	body, _ := json.Marshal(req)
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
		return nil, fmt.Errorf("%w: %s", errInsufficientFunds, string(data))
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ledger reserve returned %d: %s", resp.StatusCode, string(data))
	}
	var res LedgerReservation
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

var errInsufficientFunds = fmt.Errorf("insufficient funds")

func IsInsufficientFunds(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "insufficient funds")
}

func (c *LedgerClient) SettleReservation(ctx context.Context, reservationID uuid.UUID) (*LedgerEntry, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/ledger/reservations/"+reservationID.String()+"/settle", nil)
	if err != nil {
		return nil, err
	}
	auth.AddInternalToken(httpReq)
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ledger service unreachable: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusGone {
		return nil, fmt.Errorf("reservation expired")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ledger settle returned %d: %s", resp.StatusCode, string(data))
	}
	var entry LedgerEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

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
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ledger release returned %d: %s", resp.StatusCode, string(data))
	}
	return nil
}

// SpentToday sums the real DEBIT entries booked for the account since local
// midnight. Reads the account's actual ledger entries so card daily-limit
// checks run against money that has really moved, never a counter in memory.
func (c *LedgerClient) SpentToday(ctx context.Context, accountID uuid.UUID) (int64, error) {
	u := c.baseURL + "/v1/ledger/accounts/" + accountID.String() + "/entries?limit=500"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	auth.AddInternalToken(httpReq)
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return 0, fmt.Errorf("ledger service unreachable: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("ledger entries returned %d: %s", resp.StatusCode, string(data))
	}
	var entries []struct {
		EntryType string    `json:"entry_type"`
		Amount    int64     `json:"amount"`
		CreatedAt time.Time `json:"created_at"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var spent int64
	for _, e := range entries {
		if e.EntryType == "DEBIT" && e.CreatedAt.After(startOfDay) {
			spent += e.Amount
		}
	}
	return spent, nil
}
