package retry

import (
	"errors"
	"net/http"
	"testing"
	"time"

	domainerrors "github.com/nexora/nexora/shared/errors"
)

func TestClassifyDomainErrors(t *testing.T) {
	cases := []struct {
		err            error
		wantClass      Class
		wantRetry      bool
		wantAction     CustomerAction
		wantDeadLetter bool
	}{
		{domainerrors.ErrValidation, ClassNever, false, ActionFixRequest, false},
		{domainerrors.ErrInsufficientFunds, ClassNever, false, ActionAddMoney, false},
		{domainerrors.ErrIdempotencyConflict, ClassDuplicate, false, ActionNone, false},
		{domainerrors.ErrRateLimited, ClassDelayed, true, ActionRetryLater, false},
		{domainerrors.ErrServiceUnavailable, ClassDelayed, true, ActionRetryLater, false},
		{domainerrors.ErrUnauthorized, ClassReauth, true, ActionReauth, false},
		{domainerrors.ErrInvalidStateTransition, ClassPoison, false, ActionContactSupport, true},
		{errors.New("mystery"), ClassDelayed, true, ActionRetryLater, false},
	}
	for _, tc := range cases {
		sem := Classify(tc.err)
		if sem.Class != tc.wantClass || sem.Retryable != tc.wantRetry || sem.CustomerAction != tc.wantAction {
			t.Errorf("%v: got class=%s retry=%t action=%s, want %s/%t/%s",
				tc.err, sem.Class, sem.Retryable, sem.CustomerAction, tc.wantClass, tc.wantRetry, tc.wantAction)
		}
	}
}

func TestNeverRetriesValidation(t *testing.T) {
	a := Next(domainerrors.ErrValidation, 1)
	if !a.Exhausted || a.Delay != 0 {
		t.Fatalf("validation error must terminate immediately, got %+v", a)
	}
}

func TestPoisonGoesStraightToDLQ(t *testing.T) {
	a := Next(domainerrors.ErrInvalidStateTransition, 1)
	if !a.DeadLetter || !a.Exhausted {
		t.Fatalf("poison must dead-letter on first sight, got %+v", a)
	}
}

func TestDuplicateSuppressesRetry(t *testing.T) {
	a := Next(domainerrors.ErrIdempotencyConflict, 3)
	if !a.Exhausted || a.DeadLetter {
		t.Fatalf("duplicate must suppress without dead-lettering, got %+v", a)
	}
}

func TestDelayedRetriesAreBounded(t *testing.T) {
	policy := DefaultPolicy(ClassDelayed)
	attempt := 1
	for {
		a := Next(domainerrors.ErrServiceUnavailable, attempt)
		if a.Exhausted {
			if !a.DeadLetter {
				t.Fatal("exhausted delayed retries must dead-letter in queue contexts")
			}
			if a.N != policy.MaxAttempts {
				t.Fatalf("must stop at MaxAttempts=%d, stopped at %d", policy.MaxAttempts, a.N)
			}
			return
		}
		if a.Delay < 0 || a.Delay > policy.MaxDelay {
			t.Fatalf("backoff %v outside [0, %v]", a.Delay, policy.MaxDelay)
		}
		attempt = a.N + 1
	}
}

func TestBackoffFullJitterBoundedAndDeterministic(t *testing.T) {
	p := DefaultPolicy(ClassDelayed)
	d1 := Backoff(p, 1)
	d2 := Backoff(p, 1)
	if d1 != d2 {
		t.Fatal("backoff must be deterministic for the same attempt (reproducible tests)")
	}
	if d1 < 0 || d1 > p.MaxDelay {
		t.Fatalf("backoff %v outside bounds", d1)
	}
	// Late attempts draw from [0, MaxDelay] — the ceiling binds, the draw is
	// jittered. Verify bounds, not a specific value.
	for i := 0; i < 50; i++ {
		if got := Backoff(p, 10+i); got < 0 || got > p.MaxDelay {
			t.Fatalf("backoff %v outside [0, %v]", got, p.MaxDelay)
		}
	}
}

func TestReauthRetryOnce(t *testing.T) {
	p := DefaultPolicy(ClassReauth)
	if p.MaxAttempts != 2 {
		t.Fatalf("reauth allows exactly one refresh+retry, got MaxAttempts=%d", p.MaxAttempts)
	}
	a := Next(domainerrors.ErrUnauthorized, 1)
	if a.Exhausted {
		t.Fatal("first unauthorized must allow a reauth retry")
	}
	a = Next(domainerrors.ErrUnauthorized, 2)
	if !a.Exhausted {
		t.Fatal("second unauthorized must exhaust")
	}
}

func TestHTTPStatusPreserved(t *testing.T) {
	sem := Classify(domainerrors.ErrRateLimited)
	if sem.HTTPStatus != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", sem.HTTPStatus)
	}
}

func TestCustomErrorCarriesSemantics(t *testing.T) {
	e := New("CARD_FROZEN", false, ClassNever, ActionContactSupport, http.StatusForbidden, "card is frozen")
	sem := Classify(e)
	if sem.Code != "CARD_FROZEN" || sem.Class != ClassNever {
		t.Fatalf("custom semantics lost: %+v", sem)
	}
	if sem.HTTPStatus != http.StatusForbidden {
		t.Fatalf("status = %d", sem.HTTPStatus)
	}
}

func TestBackoffDelayAfterReauth(t *testing.T) {
	// Sanity: a reauth policy's backoff is zero (refresh immediately).
	if d := Backoff(DefaultPolicy(ClassReauth), 1); d != 0 {
		t.Fatalf("reauth backoff must be 0, got %v", d)
	}
	_ = time.Millisecond
}
