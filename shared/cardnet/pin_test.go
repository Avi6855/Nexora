package cardnet

import (
	"errors"
	"sync"
	"testing"
)

func TestPINTryCounterExactUnderConcurrency(t *testing.T) {
	store := NewMemoryPINStore()
	svc := NewPINTryService(store, 3)

	const concurrency = 50
	var wg sync.WaitGroup
	locked := make(chan struct{}, concurrency)
	successes := make(chan struct{}, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Attempt("card-1")
			switch {
			case err == nil:
				successes <- struct{}{}
			case errors.Is(err, ErrPINLocked):
				locked <- struct{}{}
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	close(successes)
	close(locked)

	okCount := len(successes)
	lockCount := len(locked)
	// Network semantics: attempts 1..maxTries-1 return success (remaining);
	// the maxTries-th attempt itself triggers "PIN try limit exceeded".
	if okCount != 2 {
		t.Fatalf("exactly maxTries-1 attempts succeed before the limit, got %d", okCount)
	}
	if lockCount != concurrency-2 {
		t.Fatalf("remaining must be locked: %d, want %d", lockCount, concurrency-2)
	}
	st, _ := svc.State("card-1")
	if st.Attempts != 3 || !st.Locked {
		t.Fatalf("final state attempts=%d locked=%t, want 3/true", st.Attempts, st.Locked)
	}
}

func TestPINTryResetAfterSuccess(t *testing.T) {
	store := NewMemoryPINStore()
	svc := NewPINTryService(store, 3)
	if _, err := svc.Attempt("c"); err != nil {
		t.Fatalf("attempt: %v", err)
	}
	if _, err := svc.Attempt("c"); err != nil {
		t.Fatalf("attempt 2: %v", err)
	}
	if err := svc.Reset("c"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	st, _ := svc.State("c")
	if st.Attempts != 0 || st.Locked {
		t.Fatalf("after reset attempts=%d locked=%t", st.Attempts, st.Locked)
	}
}

func TestPINTryLockedIsTerminal(t *testing.T) {
	store := NewMemoryPINStore()
	svc := NewPINTryService(store, 2)
	// Attempt 1 succeeds (1 remaining); attempt 2 hits the limit and locks.
	if _, err := svc.Attempt("c"); err != nil {
		t.Fatalf("attempt 1: %v", err)
	}
	if _, err := svc.Attempt("c"); !errors.Is(err, ErrPINLocked) {
		t.Fatalf("attempt 2 must trigger lock, got %v", err)
	}
	// Reset-unlock and repeat keeps exact accounting across cycles.
	if err := svc.Reset("c"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := svc.Attempt("c"); err != nil {
		t.Fatalf("re-attempt 1: %v", err)
	}
	if _, err := svc.Attempt("c"); !errors.Is(err, ErrPINLocked) {
		t.Fatal("second cycle must also lock at exactly maxTries")
	}
}
