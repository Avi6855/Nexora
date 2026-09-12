package txpolicy

import "errors"

var (
	// ErrRuleNotFound maps to HTTP 404.
	ErrRuleNotFound = errors.New("policy rule not found")
	// ErrNoPriorVersion maps to HTTP 409 (nothing to roll back to).
	ErrNoPriorVersion = errors.New("policy rule has no prior version")
)
