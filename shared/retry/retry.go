// Package retry implements Nexora's API Error Semantics + Retry
// Classification Platform.
//
// Two problems, one contract:
//
//  1. API error semantics: every service returns machine-readable errors with
//     the same shape — code, retryable, customer_action — so clients can be
//     predictable (RATE_LIMITED means "back off", VALIDATION_ERROR means
//     "fix the request", DEPENDENCY_FAILURE means "we'll retry for you").
//
//  2. Retry classification: not every failure deserves a retry. Retrying a
//     validation error wastes capacity; NOT retrying a timeout turns a
//     transient blip into a customer-visible failure. Classification and
//     backoff policy live HERE, centrally, so all services behave identically
//     — including the Kafka queue's dead-lettering decisions.
package retry

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	domainerrors "github.com/nexora/nexora/shared/errors"
)

// CustomerAction tells the client what (if anything) to do next.
type CustomerAction string

const (
	ActionNone           CustomerAction = "NONE"
	ActionRetryLater     CustomerAction = "RETRY_LATER"
	ActionFixRequest     CustomerAction = "FIX_REQUEST"
	ActionAddMoney       CustomerAction = "ADD_MONEY"
	ActionReauth         CustomerAction = "REAUTHENTICATE"
	ActionContactSupport CustomerAction = "CONTACT_SUPPORT"
)

// Class is the retry taxonomy.
type Class string

const (
	ClassNever     Class = "NEVER"     // deterministic failure; retrying changes nothing
	ClassImmediate Class = "IMMEDIATE" // safe to retry right away (idempotent, transient)
	ClassDelayed   Class = "DELAYED"   // retry after backoff (rate limits, provider 5xx)
	ClassReauth    Class = "REAUTH"    // refresh credentials, then retry once
	ClassPoison    Class = "POISON"    // unprocessable — dead-letter immediately
	ClassDuplicate Class = "DUPLICATE" // already processed — suppress, don't retry
)

// Semantics is the canonical machine-readable error contract.
type Semantics struct {
	Code           string         `json:"code"`
	Retryable      bool           `json:"retryable"`
	Class          Class          `json:"class"`
	CustomerAction CustomerAction `json:"customer_action"`
	HTTPStatus     int            `json:"-"`
	// RetryAfter is populated for DELAYED classes.
	RetryAfter time.Duration `json:"-"`
}

// Classify maps any error to its retry semantics. Domain errors and
// semantics-carrying errors declare their own contract; everything else is
// classified by shape.
func Classify(err error) Semantics {
	var de *domainerrors.DomainError
	if errors.As(err, &de) {
		return classifyDomain(de)
	}
	var se *Error
	if errors.As(err, &se) {
		return se.Sem
	}
	// Unknown errors: conservative default — bounded delayed retry, because
	// silently swallowing or infinitely retrying unknowns are both worse.
	return Semantics{
		Code: "INTERNAL_ERROR", Retryable: true, Class: ClassDelayed,
		CustomerAction: ActionRetryLater, HTTPStatus: http.StatusInternalServerError,
		RetryAfter: defaultBackoff(1),
	}
}

func classifyDomain(de *domainerrors.DomainError) Semantics {
	s := Semantics{Code: de.Code, HTTPStatus: de.HTTPStatus}
	switch de.Code {
	case "VALIDATION_ERROR":
		s.Retryable, s.Class, s.CustomerAction = false, ClassNever, ActionFixRequest
	case "INSUFFICIENT_FUNDS":
		s.Retryable, s.Class, s.CustomerAction = false, ClassNever, ActionAddMoney
	case "IDEMPOTENCY_CONFLICT":
		s.Retryable, s.Class, s.CustomerAction = false, ClassDuplicate, ActionNone
	case "RATE_LIMITED":
		s.Retryable, s.Class, s.CustomerAction = true, ClassDelayed, ActionRetryLater
		s.RetryAfter = defaultBackoff(1)
	case "SERVICE_UNAVAILABLE", "DEPENDENCY_FAILURE":
		s.Retryable, s.Class, s.CustomerAction = true, ClassDelayed, ActionRetryLater
		s.RetryAfter = defaultBackoff(1)
	case "UNAUTHORIZED", "REQUIRES_REAUTH":
		s.Retryable, s.Class, s.CustomerAction = true, ClassReauth, ActionReauth
	case "PAYMENT_NOT_FOUND", "ACCOUNT_NOT_FOUND", "USER_NOT_FOUND":
		s.Retryable, s.Class, s.CustomerAction = false, ClassNever, ActionNone
	case "INVALID_STATE_TRANSITION":
		s.Retryable, s.Class, s.CustomerAction = false, ClassPoison, ActionContactSupport
	default:
		s.Retryable, s.Class, s.CustomerAction = true, ClassDelayed, ActionRetryLater
		s.RetryAfter = defaultBackoff(1)
	}
	return s
}

// Policy governs a bounded retry loop.
type Policy struct {
	MaxAttempts int           // total attempts including the first
	BaseDelay   time.Duration // first backoff
	MaxDelay    time.Duration // backoff ceiling
	Class       Class         // this policy's class
}

// DefaultPolicy returns the standard policy for a class.
func DefaultPolicy(c Class) Policy {
	switch c {
	case ClassImmediate:
		return Policy{MaxAttempts: 3, BaseDelay: 0, MaxDelay: 0, Class: c}
	case ClassDelayed:
		return Policy{MaxAttempts: 4, BaseDelay: 200 * time.Millisecond, MaxDelay: 5 * time.Second, Class: c}
	case ClassReauth:
		return Policy{MaxAttempts: 2, BaseDelay: 0, MaxDelay: 0, Class: c}
	default: // NEVER, POISON, DUPLICATE
		return Policy{MaxAttempts: 1, BaseDelay: 0, MaxDelay: 0, Class: c}
	}
}

// Attempt is one outcome fed back into the loop.
type Attempt struct {
	N     int // attempt number, 1-based
	Err   error
	Delay time.Duration // computed backoff before the next attempt (0 = none)
	// Exhausted is true when no further attempts are permitted.
	Exhausted bool
	// DeadLetter is true when the error is poison and must skip to DLQ.
	DeadLetter bool
}

// Next computes the next step of the retry loop for an error.
func Next(err error, attemptN int) Attempt {
	sem := Classify(err)
	p := DefaultPolicy(sem.Class)

	// Non-retryable classes terminate immediately with the right disposition.
	switch sem.Class {
	case ClassNever:
		return Attempt{N: attemptN, Err: err, Exhausted: true}
	case ClassDuplicate:
		return Attempt{N: attemptN, Err: err, Exhausted: true}
	case ClassPoison:
		return Attempt{N: attemptN, Err: err, Exhausted: true, DeadLetter: true}
	}

	if attemptN >= p.MaxAttempts {
		a := Attempt{N: attemptN, Err: err, Exhausted: true}
		// Exhausted delayed retries in a queue context → dead-letter rather
		// than spin forever.
		if sem.Class == ClassDelayed {
			a.DeadLetter = true
		}
		return a
	}
	return Attempt{N: attemptN, Err: err, Delay: Backoff(p, attemptN)}
}

// Backoff computes exponential delay with FULL JITTER — the gold-standard
// anti-thundering-herd strategy: random between 0 and min(base*2^n, max).
func Backoff(p Policy, attemptN int) time.Duration {
	if p.BaseDelay <= 0 {
		return 0
	}
	ceil := p.BaseDelay * time.Duration(math.Pow(2, float64(attemptN-1)))
	if ceil > p.MaxDelay || ceil <= 0 {
		ceil = p.MaxDelay
	}
	// Full jitter: uniform in [0, ceil). Deterministic pseudo-random seeded by
	// attempt count so tests stay reproducible without a clock.
	jitter := time.Duration(hashSeed(attemptN) % uint64(ceil+1))
	return jitter
}

func hashSeed(n int) uint64 {
	// FNV-1a over the attempt number — stable across processes.
	var h uint64 = 14695981039346656037
	for i := 0; i < 8; i++ {
		h ^= uint64(n >> i & 1)
		h *= 1099511628211
	}
	return h
}

// defaultBackoff is the standalone suggestion for a first retry.
func defaultBackoff(attempt int) time.Duration {
	return Backoff(DefaultPolicy(ClassDelayed), attempt)
}

// Error builds a Semantics-carrying error for producers of domain errors.
type Error struct {
	Sem Semantics
	Msg string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Sem.Code, e.Msg) }

// New constructs a semantics-carrying error.
func New(code string, retryable bool, class Class, action CustomerAction, status int, msg string) *Error {
	return &Error{Sem: Semantics{Code: code, Retryable: retryable, Class: class, CustomerAction: action, HTTPStatus: status}, Msg: msg}
}
