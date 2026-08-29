package unit

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	nexoraerrors "github.com/nexora/nexora/shared/errors"
	"github.com/stretchr/testify/assert"
)

func TestDomainError(t *testing.T) {
	err := nexoraerrors.NewDomainError("TEST_CODE", "test message", http.StatusBadRequest)
	assert.Equal(t, "TEST_CODE", err.Code)
	assert.Equal(t, "test message", err.Message)
	assert.Equal(t, http.StatusBadRequest, err.HTTPStatus)
	assert.Equal(t, "TEST_CODE: test message", err.Error())
}

func TestDomainErrorWrapping(t *testing.T) {
	original := errors.New("original error")
	wrapped := nexoraerrors.Wrap(original, "WRAP_CODE", "wrapped message", http.StatusInternalServerError)

	assert.Equal(t, "WRAP_CODE", wrapped.Code)
	assert.Contains(t, wrapped.Message, "wrapped message")
	assert.Contains(t, wrapped.Message, "original error")
	assert.Equal(t, http.StatusInternalServerError, wrapped.HTTPStatus)
}

func TestPredefinedErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        *nexoraerrors.DomainError
		code       string
		httpStatus int
	}{
		{"InsufficientFunds", nexoraerrors.ErrInsufficientFunds, "INSUFFICIENT_FUNDS", http.StatusPaymentRequired},
		{"IdempotencyConflict", nexoraerrors.ErrIdempotencyConflict, "IDEMPOTENCY_CONFLICT", http.StatusConflict},
		{"PaymentNotFound", nexoraerrors.ErrPaymentNotFound, "PAYMENT_NOT_FOUND", http.StatusNotFound},
		{"AccountNotFound", nexoraerrors.ErrAccountNotFound, "ACCOUNT_NOT_FOUND", http.StatusNotFound},
		{"UserNotFound", nexoraerrors.ErrUserNotFound, "USER_NOT_FOUND", http.StatusNotFound},
		{"InvalidStateTransition", nexoraerrors.ErrInvalidStateTransition, "INVALID_STATE_TRANSITION", http.StatusUnprocessableEntity},
		{"Unauthorized", nexoraerrors.ErrUnauthorized, "UNAUTHORIZED", http.StatusUnauthorized},
		{"RateLimited", nexoraerrors.ErrRateLimited, "RATE_LIMITED", http.StatusTooManyRequests},
		{"ServiceUnavailable", nexoraerrors.ErrServiceUnavailable, "SERVICE_UNAVAILABLE", http.StatusServiceUnavailable},
		{"Validation", nexoraerrors.ErrValidation, "VALIDATION_ERROR", http.StatusBadRequest},
		{"Internal", nexoraerrors.ErrInternal, "INTERNAL_ERROR", http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.code, tt.err.Code)
			assert.Equal(t, tt.httpStatus, tt.err.HTTPStatus)
			assert.Contains(t, tt.err.Error(), tt.code)
		})
	}
}

func TestErrorImplementsErrorInterface(t *testing.T) {
	var err error = nexoraerrors.ErrInsufficientFunds
	assert.NotNil(t, err)
	assert.IsType(t, &nexoraerrors.DomainError{}, err)
}

func TestErrorCanBeWrapped(t *testing.T) {
	wrapped := fmt.Errorf("context: %w", nexoraerrors.ErrPaymentNotFound)
	assert.True(t, errors.Is(wrapped, nexoraerrors.ErrPaymentNotFound))
}

func TestErrorAsDomainError(t *testing.T) {
	err := fmt.Errorf("outer: %w", nexoraerrors.ErrValidation)

	var domainErr *nexoraerrors.DomainError
	assert.True(t, errors.As(err, &domainErr))
	assert.Equal(t, "VALIDATION_ERROR", domainErr.Code)
	assert.Equal(t, http.StatusBadRequest, domainErr.HTTPStatus)
}
