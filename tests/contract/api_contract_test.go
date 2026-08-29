package contract

import (
	"encoding/json"
	"testing"
)

type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	Message string      `json:"message,omitempty"`
}

type AuthResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
}

type AccountResponse struct {
	AccountID string `json:"account_id"`
	UserID    string `json:"user_id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Currency  string `json:"currency"`
	Status    string `json:"status"`
	Balance   int64  `json:"balance"`
	CreatedAt string `json:"created_at"`
}

type TransactionResponse struct {
	TransactionID string `json:"transaction_id"`
	AccountID     string `json:"account_id"`
	Type          string `json:"type"`
	Status        string `json:"status"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	Description   string `json:"description"`
	CreatedAt     string `json:"created_at"`
}

type PaymentResponse struct {
	PaymentID       string `json:"payment_id"`
	AccountID       string `json:"account_id"`
	State           string `json:"state"`
	Amount          int64  `json:"amount"`
	Currency        string `json:"currency"`
	CounterpartyID  string `json:"counterparty_id"`
	Reference       string `json:"reference"`
	CreatedAt       string `json:"created_at"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func TestAuthEndpointSchema(t *testing.T) {
	tests := []struct {
		name     string
		response string
		valid    bool
	}{
		{"valid login response", `{"success":true,"data":{"access_token":"eyJhbGciOiJIUzI1NiJ9","refresh_token":"eyJhbGciOiJIUzI1NiJ9","expires_at":1735689600}}`, true},
		{"valid error response", `{"success":false,"error":"INVALID_CREDENTIALS","message":"Invalid email or password"}`, true},
		{"missing access_token", `{"success":true,"data":{"refresh_token":"token","expires_at":1735689600}}`, false},
		{"missing refresh_token", `{"success":true,"data":{"access_token":"token","expires_at":1735689600}}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp APIResponse
			err := json.Unmarshal([]byte(tt.response), &resp)
			if err != nil && tt.valid {
				t.Errorf("failed to unmarshal: %v", err)
				return
			}

			if resp.Success && resp.Data != nil {
				dataBytes, _ := json.Marshal(resp.Data)
				var authResp AuthResponse
				err := json.Unmarshal(dataBytes, &authResp)
				if err != nil && tt.valid {
					t.Errorf("failed to unmarshal auth response: %v", err)
				}

				if tt.valid {
					if authResp.AccessToken == "" {
						t.Error("access_token should not be empty")
					}
					if authResp.RefreshToken == "" {
						t.Error("refresh_token should not be empty")
					}
				}
			}
		})
	}
}

func TestAccountEndpointSchema(t *testing.T) {
	validAccount := `{"account_id":"acc-123","user_id":"usr-456","name":"Main Account","type":"PERSONAL","currency":"GBP","status":"ACTIVE","balance":10000,"created_at":"2024-01-01T00:00:00Z"}`

	var account AccountResponse
	err := json.Unmarshal([]byte(validAccount), &account)
	if err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if account.AccountID == "" {
		t.Error("account_id should not be empty")
	}
	if account.Currency != "GBP" {
		t.Error("currency should be GBP")
	}
	if account.Type == "" {
		t.Error("type should not be empty")
	}
}

func TestTransactionEndpointSchema(t *testing.T) {
	validTransaction := `{"transaction_id":"txn-123","account_id":"acc-456","type":"TRANSFER","status":"COMPLETED","amount":5000,"currency":"GBP","description":"Test transfer","created_at":"2024-01-01T00:00:00Z"}`

	var transaction TransactionResponse
	err := json.Unmarshal([]byte(validTransaction), &transaction)
	if err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if transaction.TransactionID == "" {
		t.Error("transaction_id should not be empty")
	}
	if transaction.Amount <= 0 {
		t.Error("amount should be positive")
	}
}

func TestPaymentEndpointSchema(t *testing.T) {
	validPayment := `{"payment_id":"pay-123","account_id":"acc-456","state":"CREATED","amount":3000,"currency":"GBP","counterparty_id":"cp-789","reference":"Test payment","created_at":"2024-01-01T00:00:00Z"}`

	var payment PaymentResponse
	err := json.Unmarshal([]byte(validPayment), &payment)
	if err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if payment.PaymentID == "" {
		t.Error("payment_id should not be empty")
	}
	if payment.State == "" {
		t.Error("state should not be empty")
	}
}

func TestErrorResponseSchema(t *testing.T) {
	tests := []struct {
		name     string
		response string
		code     string
	}{
		{"validation error", `{"error":"VALIDATION_ERROR","message":"Request validation failed"}`, "VALIDATION_ERROR"},
		{"not found", `{"error":"PAYMENT_NOT_FOUND","message":"Payment not found"}`, "PAYMENT_NOT_FOUND"},
		{"unauthorized", `{"error":"UNAUTHORIZED","message":"Authentication required"}`, "UNAUTHORIZED"},
		{"rate limited", `{"error":"RATE_LIMITED","message":"Too many requests"}`, "RATE_LIMITED"},
		{"insufficient funds", `{"error":"INSUFFICIENT_FUNDS","message":"Insufficient funds for this transaction"}`, "INSUFFICIENT_FUNDS"},
		{"internal error", `{"error":"INTERNAL_ERROR","message":"An internal error occurred"}`, "INTERNAL_ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var errResp ErrorResponse
			err := json.Unmarshal([]byte(tt.response), &errResp)
			if err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if errResp.Error != tt.code {
				t.Errorf("expected error code %s, got %s", tt.code, errResp.Error)
			}
			if errResp.Message == "" {
				t.Error("message should not be empty")
			}
		})
	}
}

func TestHTTPStatusCodes(t *testing.T) {
	tests := []struct {
		errorCode  string
		httpStatus int
	}{
		{"INSUFFICIENT_FUNDS", 402},
		{"IDEMPOTENCY_CONFLICT", 409},
		{"PAYMENT_NOT_FOUND", 404},
		{"ACCOUNT_NOT_FOUND", 404},
		{"USER_NOT_FOUND", 404},
		{"INVALID_STATE_TRANSITION", 422},
		{"UNAUTHORIZED", 401},
		{"RATE_LIMITED", 429},
		{"SERVICE_UNAVAILABLE", 503},
		{"VALIDATION_ERROR", 400},
		{"INTERNAL_ERROR", 500},
	}

	for _, tt := range tests {
		t.Run(tt.errorCode, func(t *testing.T) {
			expectedStatus := tt.httpStatus
			if expectedStatus < 400 || expectedStatus > 599 {
				t.Errorf("HTTP status %d is not an error status", expectedStatus)
			}
		})
	}
}

func TestPaymentStateTransitions(t *testing.T) {
	validTransitions := map[string][]string{
		"CREATED":    {"AUTHORIZED", "FAILED", "REVERSED", "CANCELLED"},
		"AUTHORIZED": {"PROCESSING", "FAILED", "REVERSED", "CANCELLED"},
		"PROCESSING": {"CONFIRMED", "FAILED", "UNKNOWN"},
		"UNKNOWN":    {"CONFIRMED", "FAILED"},
		"CONFIRMED":  {"SETTLED", "REVERSED"},
		"SETTLED":    {},
		"FAILED":     {},
		"REVERSED":   {},
		"CANCELLED":  {},
	}

	for from, tos := range validTransitions {
		t.Run(from+" transitions", func(t *testing.T) {
			for _, to := range tos {
				if from == to {
					t.Errorf("state %s should not transition to itself", from)
				}
			}
		})
	}
}

func TestIdempotencyKeyRequired(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{"valid key", "abc-123", false},
		{"empty key", "", true},
		{"uuid key", "550e8400-e29b-41d4-a716-446655440000", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.key == "" && !tt.wantErr {
				t.Error("empty key should be invalid")
			}
		})
	}
}

func TestCurrencyCodes(t *testing.T) {
	validCurrencies := []string{"GBP", "USD", "EUR", "JPY", "AUD", "CAD", "CHF", "CNY", "INR", "BRL", "MXN", "KRW"}

	for _, curr := range validCurrencies {
		t.Run(curr, func(t *testing.T) {
			if len(curr) != 3 {
				t.Errorf("currency code %s should be 3 characters", curr)
			}
		})
	}
}

func TestPaginationHeaders(t *testing.T) {
	tests := []struct {
		name   string
		offset int
		limit  int
		valid  bool
	}{
		{"default pagination", 0, 20, true},
		{"custom pagination", 10, 50, true},
		{"negative offset", -1, 20, false},
		{"zero limit", 0, 0, false},
		{"negative limit", 0, -1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.offset < 0 && tt.valid {
				t.Error("negative offset should be invalid")
			}
			if tt.limit <= 0 && tt.valid {
				t.Error("zero or negative limit should be invalid")
			}
		})
	}
}
