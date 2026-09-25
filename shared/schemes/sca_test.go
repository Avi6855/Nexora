package schemes

import (
	"bytes"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"
)

// ── Exemption policy ────────────────────────────────────────────────────────

func TestSCAPolicyStepsUpCardNotPresentAboveLowValue(t *testing.T) {
	p := DefaultSCAPolicy()

	c := SCAContext{AmountMinor: 12_000, CardPresent: false, RiskScore: 40, MerchantCategory: "5999"}
	if !p.RequiresStepUp(c) {
		t.Fatalf("a £120 e-commerce presentment with a high fraud score must step up; got exemption %q", p.Evaluate(c))
	}
	if got := p.Evaluate(c); got != SCAExemptionNone {
		t.Fatalf("exemption = %q, want none", got)
	}
}

func TestSCAPolicyExemptions(t *testing.T) {
	p := DefaultSCAPolicy()

	cases := []struct {
		name string
		ctx  SCAContext
		want SCAExemption
	}{
		{
			name: "chip and PIN presentment is authenticated by the CVM",
			ctx:  SCAContext{AmountMinor: 90_000, CardPresent: true, RiskScore: 5},
			want: SCAExemptionCardPresent,
		},
		{
			name: "card-not-present under the low-value ceiling",
			ctx:  SCAContext{AmountMinor: 2_500, CardPresent: false, RiskScore: 80},
			want: SCAExemptionLowValue,
		},
		{
			name: "transaction-risk analysis on a low score and low-risk MCC",
			ctx:  SCAContext{AmountMinor: 20_000, CardPresent: false, RiskScore: 8, MerchantCategory: "4111"},
			want: SCAExemptionTransactionRisk,
		},
		{
			name: "transaction-risk analysis withheld when the MCC is not exempt",
			ctx:  SCAContext{AmountMinor: 20_000, CardPresent: false, RiskScore: 8, MerchantCategory: "7995"},
			want: SCAExemptionNone,
		},
		{
			name: "transaction-risk analysis withheld when the score is high",
			ctx:  SCAContext{AmountMinor: 20_000, CardPresent: false, RiskScore: 61, MerchantCategory: "4111"},
			want: SCAExemptionNone,
		},
		{
			name: "transaction-risk analysis withheld above the cap",
			ctx:  SCAContext{AmountMinor: 90_000, CardPresent: false, RiskScore: 4, MerchantCategory: "4111"},
			want: SCAExemptionNone,
		},
		{
			name: "merchant-initiated follow-on needs no new SCA",
			ctx:  SCAContext{AmountMinor: 12_000, CardPresent: false, MerchantInitiated: true},
			want: SCAExemptionMerchantInitiated,
		},
		{
			name: "trusted beneficiary",
			ctx:  SCAContext{AmountMinor: 12_000, CardPresent: false, TrustedBeneficiary: true},
			want: SCAExemptionTrustedBeneficiary,
		},
		{
			name: "cardholder-whitelisted merchant",
			ctx:  SCAContext{AmountMinor: 12_000, CardPresent: false, Whitelisted: true},
			want: SCAExemptionWhitelisted,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.Evaluate(tc.ctx); got != tc.want {
				t.Fatalf("exemption = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSCAPolicyDisabledExemptsEverything(t *testing.T) {
	p := DefaultSCAPolicy()
	p.Enabled = false
	ctx := SCAContext{AmountMinor: 40_000, CardPresent: false, RiskScore: 95}
	if got := p.Evaluate(ctx); got != SCAExemptionIssuerDisabled {
		t.Fatalf("exemption = %q, want %q", got, SCAExemptionIssuerDisabled)
	}
	if p.RequiresStepUp(ctx) {
		t.Fatal("a disabled policy still demanded step-up")
	}
}

func TestSCAPolicyCanBeConfiguredToNotRequireCardNotPresentStepUp(t *testing.T) {
	p := DefaultSCAPolicy()
	p.RequireForCardNotPresent = false
	p.LowValueExemptionMinor = 0
	p.TransactionRiskCapMinor = 0

	// With every exemption window closed the only reason this e-commerce
	// presentment escapes step-up is the explicit configuration flag.
	ctx := SCAContext{AmountMinor: 12_000, CardPresent: false, RiskScore: 90}
	if p.RequiresStepUp(ctx) {
		t.Fatal("policy configured without a card-not-present requirement still demanded step-up")
	}
}

// ── One-time codes ──────────────────────────────────────────────────────────

func TestNewOTPIsNumericAndOnlyItsDigestIsStored(t *testing.T) {
	otp, err := NewOTP(rand.Reader)
	if err != nil {
		t.Fatalf("NewOTP: %v", err)
	}
	if len(otp) != OTPDigits {
		t.Fatalf("code %q has %d digits, want %d", otp, len(otp), OTPDigits)
	}
	for _, r := range otp {
		if r < '0' || r > '9' {
			t.Fatalf("code %q is not numeric", otp)
		}
	}

	hash := HashOTP(otp)
	if hash == otp || strings.Contains(hash, otp) {
		t.Fatal("HashOTP returned the clear code")
	}
	if !VerifyOTP(hash, otp) {
		t.Fatal("a correct code did not verify against its digest")
	}
	if VerifyOTP(hash, "999999") && otp != "999999" {
		t.Fatal("a wrong code verified against the digest")
	}
}

func TestNewOTPUsesTheSuppliedSource(t *testing.T) {
	otp, err := NewOTP(bytes.NewReader([]byte{0, 0, 0, 0}))
	if err != nil {
		t.Fatalf("NewOTP: %v", err)
	}
	if otp != "000000" {
		t.Fatalf("code = %q, want 000000 from an all-zero source", otp)
	}
	if _, err := NewOTP(nil); err == nil {
		t.Fatal("NewOTP accepted a nil source")
	}
}

func TestVerifyOTPRejectsMalformedDigests(t *testing.T) {
	if VerifyOTP("", "123456") {
		t.Fatal("empty digest verified")
	}
	if VerifyOTP("not-hex", "123456") {
		t.Fatal("non-hex digest verified")
	}
}

// ── Challenge state machine ─────────────────────────────────────────────────

func TestChallengeVerifyCompletesOnceAndIsSingleUse(t *testing.T) {
	now := time.Now().UTC()
	ch := NewChallenge("ch-1", "auth-1", HashOTP("123456"), now, OTPTTL)

	if !ch.ExpiresAt.Equal(now.Add(OTPTTL)) {
		t.Fatalf("expiry = %s, want %s", ch.ExpiresAt, now.Add(OTPTTL))
	}
	if ch.Status != ChallengePending || ch.Attempts != 0 {
		t.Fatalf("new challenge is %s with %d attempts", ch.Status, ch.Attempts)
	}

	if err := ch.Verify("123456", now.Add(time.Second)); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ch.Status != ChallengeCompleted || ch.CompletedAt == nil {
		t.Fatalf("status = %s (completed_at=%v), want COMPLETED with a timestamp", ch.Status, ch.CompletedAt)
	}

	// A replay of the same (correct) code must not re-open the challenge.
	if err := ch.Verify("123456", now.Add(2*time.Second)); !errors.Is(err, ErrChallengeNotPending) {
		t.Fatalf("replay error = %v, want ErrChallengeNotPending", err)
	}
	if ch.Status != ChallengeCompleted {
		t.Fatalf("replay changed status to %s", ch.Status)
	}
}

func TestChallengeVerifyCountsAttemptsAndFailsTerminally(t *testing.T) {
	now := time.Now().UTC()
	ch := NewChallenge("ch-2", "auth-2", HashOTP("123456"), now, OTPTTL)

	for i := 1; i < MaxOTPAttempts; i++ {
		err := ch.Verify("000000", now.Add(time.Duration(i)*time.Second))
		if !errors.Is(err, ErrOTPInvalid) {
			t.Fatalf("attempt %d error = %v, want ErrOTPInvalid", i, err)
		}
		if ch.Status != ChallengePending {
			t.Fatalf("attempt %d moved the challenge to %s; a wrong code must stay retryable", i, ch.Status)
		}
		if ch.Attempts != i {
			t.Fatalf("attempts = %d after %d wrong codes", ch.Attempts, i)
		}
	}

	err := ch.Verify("000000", now.Add(time.Minute))
	if !errors.Is(err, ErrOTPAttemptsExhausted) {
		t.Fatalf("final attempt error = %v, want ErrOTPAttemptsExhausted", err)
	}
	if ch.Status != ChallengeFailed || ch.FailureReason != "attempts_exhausted" {
		t.Fatalf("status = %s / %q, want FAILED/attempts_exhausted", ch.Status, ch.FailureReason)
	}

	// The correct code cannot rescue a failed challenge.
	if err := ch.Verify("123456", now.Add(2*time.Minute)); !errors.Is(err, ErrChallengeNotPending) {
		t.Fatalf("post-failure error = %v, want ErrChallengeNotPending", err)
	}
}

func TestChallengeVerifyRejectsExpiredChallenges(t *testing.T) {
	now := time.Now().UTC()
	ch := NewChallenge("ch-3", "auth-3", HashOTP("123456"), now, time.Minute)

	err := ch.Verify("123456", now.Add(2*time.Minute))
	if !errors.Is(err, ErrChallengeExpired) {
		t.Fatalf("error = %v, want ErrChallengeExpired", err)
	}
	if ch.Status != ChallengeExpired || ch.FailureReason != "challenge_expired" {
		t.Fatalf("status = %s / %q, want EXPIRED/challenge_expired", ch.Status, ch.FailureReason)
	}
	// An expired challenge never succeeds, even with the right code.
	if err := ch.Verify("123456", now.Add(3*time.Minute)); !errors.Is(err, ErrChallengeNotPending) {
		t.Fatalf("error after expiry = %v, want ErrChallengeNotPending", err)
	}
}

func TestChallengeExpireClosesAnAbandonedStepUp(t *testing.T) {
	now := time.Now().UTC()
	ch := NewChallenge("ch-5", "auth-5", HashOTP("123456"), now, time.Minute)

	// A live challenge is not expired by the sweep.
	if err := ch.Expire(now); !errors.Is(err, ErrChallengeNotExpired) {
		t.Fatalf("expiring a live challenge error = %v, want ErrChallengeNotExpired", err)
	}
	if ch.Status != ChallengePending {
		t.Fatalf("a refused expiry moved the challenge to %s", ch.Status)
	}

	at := now.Add(2 * time.Minute)
	if err := ch.Expire(at); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if ch.Status != ChallengeExpired || ch.FailureReason != "challenge_expired" || ch.CompletedAt != nil {
		t.Fatalf("expired challenge = %s/%q (completed_at=%v)", ch.Status, ch.FailureReason, ch.CompletedAt)
	}

	// An expired challenge is terminal: neither a late code nor a second sweep
	// may reopen it.
	if err := ch.Verify("123456", at.Add(time.Second)); !errors.Is(err, ErrChallengeNotPending) {
		t.Fatalf("late code error = %v, want ErrChallengeNotPending", err)
	}
	if err := ch.Expire(at.Add(time.Minute)); !errors.Is(err, ErrChallengeNotPending) {
		t.Fatalf("second expiry error = %v, want ErrChallengeNotPending", err)
	}
}

func TestChallengeDefaultsMaxAttemptsAndTTL(t *testing.T) {
	now := time.Now().UTC()
	ch := NewChallenge("ch-4", "auth-4", HashOTP("123456"), now, 0)
	if ch.MaxAttempts != MaxOTPAttempts {
		t.Fatalf("max attempts = %d, want %d", ch.MaxAttempts, MaxOTPAttempts)
	}
	if !ch.ExpiresAt.Equal(now.Add(OTPTTL)) {
		t.Fatalf("expiry = %s, want the default TTL", ch.ExpiresAt)
	}
}
