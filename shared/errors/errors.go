package errors

import (
	"fmt"
	"net/http"
)

type DomainError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"-"`
}

func (e *DomainError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func NewDomainError(code string, message string, httpStatus int) *DomainError {
	return &DomainError{
		Code:       code,
		Message:    message,
		HTTPStatus: httpStatus,
	}
}

func Wrap(err error, code string, message string, httpStatus int) *DomainError {
	return &DomainError{
		Code:       code,
		Message:    fmt.Sprintf("%s: %v", message, err),
		HTTPStatus: httpStatus,
	}
}

var (
	ErrInsufficientFunds = &DomainError{
		Code:       "INSUFFICIENT_FUNDS",
		Message:    "insufficient funds for this transaction",
		HTTPStatus: http.StatusPaymentRequired,
	}

	ErrIdempotencyConflict = &DomainError{
		Code:       "IDEMPOTENCY_CONFLICT",
		Message:    "request already processed with different payload",
		HTTPStatus: http.StatusConflict,
	}

	ErrPaymentNotFound = &DomainError{
		Code:       "PAYMENT_NOT_FOUND",
		Message:    "payment not found",
		HTTPStatus: http.StatusNotFound,
	}

	ErrAccountNotFound = &DomainError{
		Code:       "ACCOUNT_NOT_FOUND",
		Message:    "account not found",
		HTTPStatus: http.StatusNotFound,
	}

	ErrUserNotFound = &DomainError{
		Code:       "USER_NOT_FOUND",
		Message:    "user not found",
		HTTPStatus: http.StatusNotFound,
	}

	ErrInvalidStateTransition = &DomainError{
		Code:       "INVALID_STATE_TRANSITION",
		Message:    "invalid state transition for this entity",
		HTTPStatus: http.StatusUnprocessableEntity,
	}

	ErrUnauthorized = &DomainError{
		Code:       "UNAUTHORIZED",
		Message:    "authentication required or token invalid",
		HTTPStatus: http.StatusUnauthorized,
	}

	ErrRateLimited = &DomainError{
		Code:       "RATE_LIMITED",
		Message:    "too many requests, please try again later",
		HTTPStatus: http.StatusTooManyRequests,
	}

	ErrServiceUnavailable = &DomainError{
		Code:       "SERVICE_UNAVAILABLE",
		Message:    "service temporarily unavailable",
		HTTPStatus: http.StatusServiceUnavailable,
	}

	ErrValidation = &DomainError{
		Code:       "VALIDATION_ERROR",
		Message:    "request validation failed",
		HTTPStatus: http.StatusBadRequest,
	}

	ErrInternal = &DomainError{
		Code:       "INTERNAL_ERROR",
		Message:    "an internal error occurred",
		HTTPStatus: http.StatusInternalServerError,
	}
)
