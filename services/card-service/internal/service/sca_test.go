package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nexora/nexora/services/card-service/internal/domain"
	"github.com/nexora/nexora/services/card-service/internal/repository"
	"github.com/nexora/nexora/shared/schemes"
)

// ── Challenge store double ──────────────────────────────────────────────────

type fakeChallengeRepo struct {
	mu     sync.Mutex
	byID   map[uuid.UUID]*domain.SCAChallenge
	order  []uuid.UUID
	expiry map[string][]domain.SCAExpiring
	// listErr injects an index read failure (sweep error handling).
	listErr error
}

func newFakeChallengeRepo() *fakeChallengeRepo {
	return &fakeChallengeRepo{
		byID:   make(map[uuid.UUID]*domain.SCAChallenge),
		expiry: make(map[string][]domain.SCAExpiring),
	}
}

func (r *fakeChallengeRepo) Create(ctx context.Context, ch *domain.SCAChallenge) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	clone := *ch
	r.byID[ch.ChallengeID] = &clone
	r.order = append(r.order, ch.ChallengeID)
	// The time-bucketed index row, exactly as the Cassandra repository writes it.
	r.expiry[domain.ExpiryBucket(ch.ExpiresAt)] = append(r.expiry[domain.ExpiryBucket(ch.ExpiresAt)], domain.SCAExpiring{
		Bucket:      domain.ExpiryBucket(ch.ExpiresAt),
		ExpiresAt:   ch.ExpiresAt,
		ChallengeID: ch.ChallengeID,
	})
	return nil
}

func (r *fakeChallengeRepo) ListDueExpiring(ctx context.Context, buckets []string, now time.Time) ([]domain.SCAExpiring, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listErr != nil {
		return nil, r.listErr
	}
	var due []domain.SCAExpiring
	for _, bucket := range buckets {
		for _, entry := range r.expiry[bucket] {
			if !entry.ExpiresAt.After(now) {
				due = append(due, entry)
			}
		}
	}
	return due, nil
}

func (r *fakeChallengeRepo) ClearExpiry(ctx context.Context, entry domain.SCAExpiring) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := make([]domain.SCAExpiring, 0, len(r.expiry[entry.Bucket]))
	for _, e := range r.expiry[entry.Bucket] {
		if e.ChallengeID != entry.ChallengeID {
			kept = append(kept, e)
		}
	}
	r.expiry[entry.Bucket] = kept
	return nil
}

// indexSize reports how many expiry-index rows are still pending sweep.
func (r *fakeChallengeRepo) indexSize() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	total := 0
	for _, entries := range r.expiry {
		total += len(entries)
	}
	return total
}

func (r *fakeChallengeRepo) setListErr(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listErr = err
}

func (r *fakeChallengeRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.SCAChallenge, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrChallengeNotFound
	}
	clone := *ch
	return &clone, nil
}

// Transition mirrors the Cassandra LWT: it applies only while the stored row is
// still PENDING at the attempt count the caller computed from.
func (r *fakeChallengeRepo) Transition(ctx context.Context, ch *domain.SCAChallenge, expectedAttempts int) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored, ok := r.byID[ch.ChallengeID]
	if !ok {
		return false, domain.ErrChallengeNotFound
	}
	if stored.Status != schemes.ChallengePending || stored.Attempts != expectedAttempts {
		return false, nil
	}
	stored.Attempts = ch.Attempts
	stored.Status = ch.Status
	stored.FailureReason = ch.FailureReason
	stored.UpdatedAt = ch.UpdatedAt
	stored.CompletedAt = ch.CompletedAt
	return true, nil
}

func (r *fakeChallengeRepo) last() *domain.SCAChallenge {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.order) == 0 {
		return nil
	}
	clone := *r.byID[r.order[len(r.order)-1]]
	return &clone
}

func (r *fakeChallengeRepo) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.order)
}

// forceExpire pushes a challenge past its expiry, the way an abandoned
// challenge reaches its TTL in production.
func (r *fakeChallengeRepo) forceExpire(id uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ch, ok := r.byID[id]; ok {
		ch.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	}
}

var _ repository.SCAChallengeRepository = (*fakeChallengeRepo)(nil)

// ── Helpers ─────────────────────────────────────────────────────────────────

// ecommercePresentment is a card-not-present presentment (merchant presentments
// arrive with the ECOM entry mode).
func ecommercePresentment(amount int64) *domain.AuthorizeCardRequest {
	req := presentment()
	req.Amount = amount
	req.Merchant = "Zara Online"
	req.MerchantCategory = "5651"
	req.TerminalID = "ECOM"
	return req
}

func stepUp(t *testing.T, h *harness, card *domain.Card, req *domain.AuthorizeCardRequest) *domain.CardAuthorization {
	t.Helper()
	auth, err := h.svc.AuthorizeCard(context.Background(), card.CardID, req)
	if err != nil {
		t.Fatalf("AuthorizeCard: %v", err)
	}
	return auth
}

// ── Raising the step-up ─────────────────────────────────────────────────────

func TestCardNotPresentAboveLowValueStepsUpWithoutHoldingFunds(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())

	auth := stepUp(t, h, card, ecommercePresentment(120_00))

	if auth.Status != domain.AuthStatusChallenged {
		t.Fatalf("status = %s, want CHALLENGED", auth.Status)
	}
	if auth.Decision != domain.AuthDecisionChallenge {
		t.Fatalf("decision = %s, want CHALLENGE", auth.Decision)
	}
	if auth.ChallengeID == uuid.Nil {
		t.Fatal("challenged authorization carries no challenge id")
	}
	if auth.ReservationID != uuid.Nil {
		t.Fatalf("challenged authorization holds %s before the cardholder authenticated", auth.ReservationID)
	}
	if reserves, _, _ := h.ledger.counts(); reserves != 0 {
		t.Fatalf("ledger held funds (%d reservations) while the cardholder was still authenticating", reserves)
	}

	challenge := h.lastChallenge()
	if challenge == nil {
		t.Fatal("no step-up was persisted")
	}
	if challenge.AuthorizationID != auth.AuthorizationID || challenge.CardID != card.CardID {
		t.Fatal("the step-up is not bound to the presentment it was raised for")
	}
	if challenge.Status != schemes.ChallengePending {
		t.Fatalf("challenge status = %s, want PENDING", challenge.Status)
	}
	if challenge.Amount != 120_00 || challenge.Merchant != "Zara Online" {
		t.Fatalf("challenge is not bound to the presented amount/merchant: %d %q", challenge.Amount, challenge.Merchant)
	}
	if challenge.OTPHash == "" || challenge.OTPHash == h.otp {
		t.Fatal("the one-time code was stored in the clear")
	}
	if challenge.MaxAttempts != schemes.MaxOTPAttempts {
		t.Fatalf("max attempts = %d, want %d", challenge.MaxAttempts, schemes.MaxOTPAttempts)
	}
}

func TestExemptPresentmentsDoNotStepUp(t *testing.T) {
	cases := []struct {
		name       string
		market     func(*domain.AuthorizeCardRequest)
		wantExempt schemes.SCAExemption
	}{
		{
			name:       "chip and PIN terminal presentment",
			market:     func(r *domain.AuthorizeCardRequest) {}, // POS terminal from presentment()
			wantExempt: schemes.SCAExemptionCardPresent,
		},
		{
			name: "card-not-present under the low-value ceiling",
			market: func(r *domain.AuthorizeCardRequest) {
				r.Amount = 25_00
				r.Merchant = "Zara Online"
				r.TerminalID = "ECOM"
			},
			wantExempt: schemes.SCAExemptionLowValue,
		},
		{
			name: "transaction-risk analysis on a low score and exempt MCC",
			market: func(r *domain.AuthorizeCardRequest) {
				r.Amount = 200_00
				r.Merchant = "Zara Online"
				r.TerminalID = "ECOM"
				r.MerchantCategory = "4111"
			},
			wantExempt: schemes.SCAExemptionTransactionRisk,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			card := h.withCard(activeCard())

			req := presentment()
			tc.market(req)

			auth := stepUp(t, h, card, req)
			if auth.Status != domain.AuthStatusApproved {
				t.Fatalf("status = %s, want APPROVED for an exempted presentment", auth.Status)
			}
			if auth.SCAExemption != string(tc.wantExempt) {
				t.Fatalf("exemption = %q, want %q", auth.SCAExemption, tc.wantExempt)
			}
			if h.challenges.count() != 0 {
				t.Fatal("an exempted presentment still raised a step-up")
			}
			if reserves, _, _ := h.ledger.counts(); reserves != 1 {
				t.Fatalf("ledger reserves = %d, want 1", reserves)
			}
		})
	}
}

func TestMerchantInitiatedClaimNeedsAPriorCapturedPresentment(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())

	// No history with this merchant: the merchant cannot exempt itself.
	claim := ecommercePresentment(120_00)
	claim.MerchantInitiated = true
	auth := stepUp(t, h, card, claim)
	if auth.Status != domain.AuthStatusChallenged {
		t.Fatalf("status = %s, want CHALLENGED when the merchant-initiated claim has no prior payment", auth.Status)
	}

	// Capture a first presentment at the merchant, then the follow-on bill is
	// exempt exactly as PSD2 intends.
	if _, err := h.svc.CompleteSCAChallenge(context.Background(), card.CardID, auth.AuthorizationID, &domain.CompleteSCAChallengeRequest{
		ChallengeID: auth.ChallengeID,
		OTP:         h.otp,
	}); err != nil {
		t.Fatalf("CompleteSCAChallenge: %v", err)
	}
	if _, err := h.svc.CaptureAuthorization(context.Background(), card.CardID, auth.AuthorizationID); err != nil {
		t.Fatalf("CaptureAuthorization: %v", err)
	}

	followOn := ecommercePresentment(60_00)
	followOn.MerchantInitiated = true
	second := stepUp(t, h, card, followOn)

	if second.Status != domain.AuthStatusApproved {
		t.Fatalf("status = %s, want APPROVED for a merchant-initiated follow-on", second.Status)
	}
	if second.SCAExemption != string(schemes.SCAExemptionMerchantInitiated) {
		t.Fatalf("exemption = %q, want %q", second.SCAExemption, schemes.SCAExemptionMerchantInitiated)
	}
}

func TestSCADisabledByPolicyApprovesWithoutAChallenge(t *testing.T) {
	h := newHarness(t)
	h.disableSCA()
	card := h.withCard(activeCard())

	auth := stepUp(t, h, card, ecommercePresentment(500_00))

	if auth.Status != domain.AuthStatusApproved {
		t.Fatalf("status = %s, want APPROVED with the issuer policy switched off", auth.Status)
	}
	if h.challenges.count() != 0 {
		t.Fatal("a disabled policy still raised a step-up")
	}
}

// ── Answering the step-up ───────────────────────────────────────────────────

func TestCorrectCodeApprovesAndOnlyThenHoldsFunds(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())
	auth := stepUp(t, h, card, ecommercePresentment(120_00))

	approved, err := h.svc.CompleteSCAChallenge(context.Background(), card.CardID, auth.AuthorizationID, &domain.CompleteSCAChallengeRequest{
		ChallengeID: auth.ChallengeID,
		OTP:         h.otp,
	})
	if err != nil {
		t.Fatalf("CompleteSCAChallenge: %v", err)
	}

	if approved.Status != domain.AuthStatusApproved || approved.Decision != domain.AuthDecisionApprove {
		t.Fatalf("status/decision = %s/%s, want APPROVED/APPROVE", approved.Status, approved.Decision)
	}
	if approved.ReservationID == uuid.Nil {
		t.Fatal("approved step-up has no funds hold")
	}
	if reserves, _, _ := h.ledger.counts(); reserves != 1 {
		t.Fatalf("ledger reserves = %d, want exactly 1 at the moment the step-up was answered", reserves)
	}

	challenge := h.lastChallenge()
	if challenge.Status != schemes.ChallengeCompleted || challenge.CompletedAt == nil {
		t.Fatalf("challenge status = %s (completed_at=%v), want COMPLETED", challenge.Status, challenge.CompletedAt)
	}

	// The code is single use: replaying the same correct answer must not take a
	// second hold, and must not be able to reach the ledger again.
	if _, err := h.svc.CompleteSCAChallenge(context.Background(), card.CardID, auth.AuthorizationID, &domain.CompleteSCAChallengeRequest{
		ChallengeID: auth.ChallengeID,
		OTP:         h.otp,
	}); err == nil {
		t.Fatal("a replayed step-up answer was accepted")
	}
	if reserves, _, _ := h.ledger.counts(); reserves != 1 {
		t.Fatalf("ledger reserves = %d after a replayed answer, want 1", reserves)
	}
}

func TestWrongCodesConsumeAttemptsThenDeclineWithoutHoldingFunds(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())
	auth := stepUp(t, h, card, ecommercePresentment(120_00))

	for i := 1; i < schemes.MaxOTPAttempts; i++ {
		_, err := h.svc.CompleteSCAChallenge(context.Background(), card.CardID, auth.AuthorizationID, &domain.CompleteSCAChallengeRequest{
			ChallengeID: auth.ChallengeID,
			OTP:         "000000",
		})
		if !errors.Is(err, domain.ErrChallengeOTP) {
			t.Fatalf("attempt %d error = %v, want ErrChallengeOTP", i, err)
		}
		if got := h.lastChallenge(); got.Status != schemes.ChallengePending || got.Attempts != i {
			t.Fatalf("after %d wrong codes the challenge is %s with %d attempts", i, got.Status, got.Attempts)
		}
		if stored, _ := h.auths.GetByID(context.Background(), auth.AuthorizationID); stored.Status != domain.AuthStatusChallenged {
			t.Fatalf("a retryable wrong code moved the authorization to %s", stored.Status)
		}
		if reserves, _, _ := h.ledger.counts(); reserves != 0 {
			t.Fatal("a wrong code still held funds")
		}
	}

	declined, err := h.svc.CompleteSCAChallenge(context.Background(), card.CardID, auth.AuthorizationID, &domain.CompleteSCAChallengeRequest{
		ChallengeID: auth.ChallengeID,
		OTP:         "000000",
	})
	if err != nil {
		t.Fatalf("final attempt returned error %v, want a declined authorization", err)
	}
	if declined.Status != domain.AuthStatusDeclined || declined.DeclineReason != "sca_failed" {
		t.Fatalf("status/reason = %s/%q, want DECLINED/sca_failed", declined.Status, declined.DeclineReason)
	}
	if reserves, _, _ := h.ledger.counts(); reserves != 0 {
		t.Fatalf("ledger reserves = %d after a failed step-up, want 0", reserves)
	}
	if got := h.lastChallenge(); got.Status != schemes.ChallengeFailed {
		t.Fatalf("challenge status = %s, want FAILED", got.Status)
	}

	// The correct code cannot rescue a failed challenge.
	if _, err := h.svc.CompleteSCAChallenge(context.Background(), card.CardID, auth.AuthorizationID, &domain.CompleteSCAChallengeRequest{
		ChallengeID: auth.ChallengeID,
		OTP:         h.otp,
	}); !errors.Is(err, domain.ErrAuthNotChallenged) {
		t.Fatalf("error after exhaustion = %v, want ErrAuthNotChallenged", err)
	}
}

func TestExpiredChallengeDeclinesWithoutHoldingFunds(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())
	auth := stepUp(t, h, card, ecommercePresentment(120_00))
	h.challenges.forceExpire(auth.ChallengeID)

	declined, err := h.svc.CompleteSCAChallenge(context.Background(), card.CardID, auth.AuthorizationID, &domain.CompleteSCAChallengeRequest{
		ChallengeID: auth.ChallengeID,
		OTP:         h.otp,
	})
	if err != nil {
		t.Fatalf("CompleteSCAChallenge on an expired challenge: %v", err)
	}
	if declined.Status != domain.AuthStatusDeclined || declined.DeclineReason != "sca_challenge_expired" {
		t.Fatalf("status/reason = %s/%q, want DECLINED/sca_challenge_expired", declined.Status, declined.DeclineReason)
	}
	if got := h.lastChallenge(); got.Status != schemes.ChallengeExpired {
		t.Fatalf("challenge status = %s, want EXPIRED", got.Status)
	}
	if reserves, _, _ := h.ledger.counts(); reserves != 0 {
		t.Fatal("an expired challenge still held funds")
	}
}

func TestStepUpAnsweredButLedgerRefusingDeclinesWithoutAnOrphanHold(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())
	auth := stepUp(t, h, card, ecommercePresentment(120_00))
	h.ledger.setReserveStatus(http.StatusServiceUnavailable)

	declined, err := h.svc.CompleteSCAChallenge(context.Background(), card.CardID, auth.AuthorizationID, &domain.CompleteSCAChallengeRequest{
		ChallengeID: auth.ChallengeID,
		OTP:         h.otp,
	})
	if err != nil {
		t.Fatalf("CompleteSCAChallenge: %v", err)
	}
	if declined.Status != domain.AuthStatusDeclined || declined.DeclineReason != "ledger_unavailable" {
		t.Fatalf("status/reason = %s/%q, want DECLINED/ledger_unavailable", declined.Status, declined.DeclineReason)
	}
	if declined.ReservationID != uuid.Nil {
		t.Fatal("a declined step-up carries a reservation")
	}
}

func TestChallengeOnlyAuthenticatesItsOwnPresentment(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())

	first := stepUp(t, h, card, ecommercePresentment(120_00))
	second := stepUp(t, h, card, ecommercePresentment(9_000))

	// Answering the first presentment with the second presentment's challenge
	// must not authenticate anything.
	if _, err := h.svc.CompleteSCAChallenge(context.Background(), card.CardID, first.AuthorizationID, &domain.CompleteSCAChallengeRequest{
		ChallengeID: second.ChallengeID,
		OTP:         h.otp,
	}); !errors.Is(err, domain.ErrChallengeNotFound) {
		t.Fatalf("cross-presentment answer error = %v, want ErrChallengeNotFound", err)
	}
	if reserves, _, _ := h.ledger.counts(); reserves != 0 {
		t.Fatal("a cross-presentment answer held funds")
	}

	// And a card that does not own the presentment cannot answer it at all.
	if _, err := h.svc.CompleteSCAChallenge(context.Background(), uuid.New(), first.AuthorizationID, &domain.CompleteSCAChallengeRequest{
		ChallengeID: first.ChallengeID,
		OTP:         h.otp,
	}); !errors.Is(err, domain.ErrAuthCardMismatch) {
		t.Fatalf("cross-card answer error = %v, want ErrAuthCardMismatch", err)
	}
}

func TestChallengedPresentmentCannotBeCapturedOrVoided(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())
	auth := stepUp(t, h, card, ecommercePresentment(120_00))

	if _, err := h.svc.CaptureAuthorization(context.Background(), card.CardID, auth.AuthorizationID); !errors.Is(err, domain.ErrAuthNotCapturable) {
		t.Fatalf("capture error = %v, want ErrAuthNotCapturable", err)
	}
	if _, err := h.svc.VoidAuthorization(context.Background(), card.CardID, auth.AuthorizationID); !errors.Is(err, domain.ErrAuthNotVoidable) {
		t.Fatalf("void error = %v, want ErrAuthNotVoidable", err)
	}
	if _, settles, releases := h.ledger.counts(); settles != 0 || releases != 0 {
		t.Fatalf("a challenged presentment moved money: settles=%d releases=%d", settles, releases)
	}
}

// ── Expiry sweep ────────────────────────────────────────────────────────────

func TestExpiryBucketsCoverTheChallengeTTL(t *testing.T) {
	now := time.Now().UTC()
	buckets := expiryBuckets(now)
	has := func(t time.Time) bool {
		want := domain.ExpiryBucket(t)
		for _, b := range buckets {
			if b == want {
				return true
			}
		}
		return false
	}

	if !has(now) {
		t.Fatal("the current bucket is not swept, so a challenge expiring now would never close")
	}
	if !has(now.Add(-schemes.OTPTTL)) {
		t.Fatal("the bucket holding a challenge that just expired is not swept")
	}
	if has(now.Add(-24 * time.Hour)) {
		t.Fatal("the sweep window is unbounded; it would re-read the entire index")
	}
}

func TestExpirySweepDeclinesAnUnansweredStepUp(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())
	auth := stepUp(t, h, card, ecommercePresentment(120_00))

	closed, err := h.svc.SweepExpiredChallenges(context.Background(), time.Now().UTC().Add(10*time.Minute))
	if err != nil {
		t.Fatalf("SweepExpiredChallenges: %v", err)
	}
	if closed != 1 {
		t.Fatalf("closed %d step-ups, want 1", closed)
	}

	stored, err := h.auths.GetByID(context.Background(), auth.AuthorizationID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.Status != domain.AuthStatusDeclined || stored.DeclineReason != "sca_challenge_expired" {
		t.Fatalf("authorization = %s/%q, want DECLINED/sca_challenge_expired", stored.Status, stored.DeclineReason)
	}
	if stored.ReservationID != uuid.Nil {
		t.Fatal("an expired step-up took a funds hold")
	}
	if reserves, _, _ := h.ledger.counts(); reserves != 0 {
		t.Fatal("the sweep held the customer's money")
	}
	if got := h.lastChallenge(); got.Status != schemes.ChallengeExpired {
		t.Fatalf("challenge status = %s, want EXPIRED", got.Status)
	}
	if h.challenges.indexSize() != 0 {
		t.Fatal("the swept step-up is still in the expiry index and would be re-read forever")
	}

	// Sweeping again is a no-op: the transition is single use.
	closed, err = h.svc.SweepExpiredChallenges(context.Background(), time.Now().UTC().Add(20*time.Minute))
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if closed != 0 {
		t.Fatalf("a repeated sweep closed %d step-ups, want 0", closed)
	}
}

func TestExpirySweepLeavesLiveAndAnsweredStepUpsAlone(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())

	live := stepUp(t, h, card, ecommercePresentment(120_00))
	closed, err := h.svc.SweepExpiredChallenges(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("SweepExpiredChallenges: %v", err)
	}
	if closed != 0 {
		t.Fatalf("the sweep closed %d live step-ups", closed)
	}
	stored, _ := h.auths.GetByID(context.Background(), live.AuthorizationID)
	if stored.Status != domain.AuthStatusChallenged {
		t.Fatalf("a live step-up is now %s", stored.Status)
	}

	// A step-up the cardholder answered keeps its outcome, and its index entry
	// is retired so the sweep stops re-reading it.
	answered := stepUp(t, h, card, ecommercePresentment(30_000))
	if _, err := h.svc.CompleteSCAChallenge(context.Background(), card.CardID, answered.AuthorizationID, &domain.CompleteSCAChallengeRequest{
		ChallengeID: answered.ChallengeID,
		OTP:         h.otp,
	}); err != nil {
		t.Fatalf("CompleteSCAChallenge: %v", err)
	}

	closed, err = h.svc.SweepExpiredChallenges(context.Background(), time.Now().UTC().Add(10*time.Minute))
	if err != nil {
		t.Fatalf("SweepExpiredChallenges: %v", err)
	}
	if closed != 1 {
		t.Fatalf("closed %d step-ups, want only the unanswered one", closed)
	}
	approved, _ := h.auths.GetByID(context.Background(), answered.AuthorizationID)
	if approved.Status != domain.AuthStatusApproved {
		t.Fatalf("an answered step-up was changed to %s by the sweep", approved.Status)
	}
	if h.challenges.indexSize() != 0 {
		t.Fatalf("the expiring index still holds %d settled step-ups", h.challenges.indexSize())
	}
}

func TestExpirySweepSurfacesItsOwnFailures(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())
	stepUp(t, h, card, ecommercePresentment(120_00))

	h.challenges.setListErr(errors.New("cassandra unavailable"))
	closed, err := h.svc.SweepExpiredChallenges(context.Background(), time.Now().UTC().Add(10*time.Minute))
	if err == nil {
		t.Fatal("a broken expiry index was reported as a clean sweep")
	}
	if closed != 0 {
		t.Fatalf("closed = %d, want 0 on a failed read", closed)
	}
}

func TestChallengeStateIsReadableWithoutLeakingTheCode(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())
	auth := stepUp(t, h, card, ecommercePresentment(120_00))

	challenge, err := h.svc.GetSCAChallenge(context.Background(), auth.AuthorizationID)
	if err != nil {
		t.Fatalf("GetSCAChallenge: %v", err)
	}
	if challenge.Status != schemes.ChallengePending || challenge.Attempts != 0 {
		t.Fatalf("challenge = %s/%d, want PENDING/0", challenge.Status, challenge.Attempts)
	}

	encoded, err := json.Marshal(challenge)
	if err != nil {
		t.Fatalf("marshaling challenge: %v", err)
	}
	if strings.Contains(string(encoded), "otp") {
		t.Fatalf("the challenge response exposes the code material: %s", encoded)
	}

	// A presentment without a step-up has no challenge to read.
	if _, err := h.svc.GetSCAChallenge(context.Background(), uuid.New()); !errors.Is(err, domain.ErrAuthNotFound) {
		t.Fatalf("unknown authorization error = %v, want ErrAuthNotFound", err)
	}
}
