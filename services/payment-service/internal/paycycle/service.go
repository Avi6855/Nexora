package paycycle

import (
	"sync"
	"time"

	sharedpaycycle "github.com/nexora/nexora/shared/paycycle"
)

// Service is the payment-service's in-memory pay-cycle store: one rail
// calendar, the beneficiary directory and the approval engine, all backed by
// shared/paycycle. The mutex guards the mutex-free shared stores.
type Service struct {
	mu            sync.RWMutex
	calendar      *sharedpaycycle.Calendar
	beneficiaries *sharedpaycycle.Store
	approvals     *sharedpaycycle.Engine
}

// NewService creates a pay-cycle service in loc (nil means UTC).
func NewService(loc *time.Location) *Service {
	if loc == nil {
		loc = time.UTC
	}
	return &Service{
		calendar:      sharedpaycycle.NewCalendar(loc),
		beneficiaries: sharedpaycycle.NewStore(),
		approvals:     sharedpaycycle.NewEngine(),
	}
}

// SetHolidays replaces the calendar's bank-holiday set (YYYY-MM-DD).
func (s *Service) SetHolidays(dates []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calendar.SetHolidays(dates)
}

// QuoteETA promises an arrival time on rail for amountMinor as of now.
func (s *Service) QuoteETA(rail string, amountMinor int64, now time.Time) (sharedpaycycle.ETAQuote, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.calendar.QuoteETA(sharedpaycycle.Rail(rail), amountMinor, now)
}

// AddBeneficiary saves a new CREATED beneficiary.
func (s *Service) AddBeneficiary(ownerID, name, sortCode, accountNumber string, now time.Time) (*sharedpaycycle.Beneficiary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.beneficiaries.Add(ownerID, name, sortCode, accountNumber, now)
}

// VerifyBeneficiary advances beneficiary trust one step.
func (s *Service) VerifyBeneficiary(id string) (*sharedpaycycle.Beneficiary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.beneficiaries.Verify(id)
}

// RecordPayment records a successful payment to a beneficiary.
func (s *Service) RecordPayment(id string, now time.Time) (*sharedpaycycle.Beneficiary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.beneficiaries.MarkPaid(id, now)
}

// UpdateBeneficiaryDetails replaces account details, resetting trust and
// starting cooling when the fingerprint changes.
func (s *Service) UpdateBeneficiaryDetails(id, sortCode, accountNumber string, now time.Time) (bool, *sharedpaycycle.Beneficiary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.beneficiaries.UpdateDetails(id, sortCode, accountNumber, now)
}

// MarkDormant parks a beneficiary as DORMANT.
func (s *Service) MarkDormant(id string) (*sharedpaycycle.Beneficiary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.beneficiaries.TouchDormant(id)
}

// RemoveBeneficiary tombstones a beneficiary.
func (s *Service) RemoveBeneficiary(id string) (*sharedpaycycle.Beneficiary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.beneficiaries.Remove(id)
}

// BeneficiaryStatus returns one beneficiary.
func (s *Service) BeneficiaryStatus(id string) (*sharedpaycycle.Beneficiary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.beneficiaries.Status(id)
}

// AddApprovalRule stores an approval rule.
func (s *Service) AddApprovalRule(rule sharedpaycycle.ApprovalRule) (sharedpaycycle.ApprovalRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.approvals.AddRule(rule)
}

// EvaluatePayment returns the approval decision for an intent.
func (s *Service) EvaluatePayment(intent sharedpaycycle.PaymentIntent) (sharedpaycycle.Decision, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.approvals.Evaluate(intent)
}

// RequestApproval opens a PENDING approval request.
func (s *Service) RequestApproval(intent sharedpaycycle.PaymentIntent, ttl time.Duration, now time.Time) (*sharedpaycycle.ApprovalRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.approvals.RequestApproval(intent, ttl, now)
}

// DecideApproval approves or rejects a PENDING request.
func (s *Service) DecideApproval(id string, approve bool, now time.Time) (*sharedpaycycle.ApprovalRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.approvals.DecideApproval(id, approve, now)
}

// GetApproval returns one approval request (lazily expiring it).
func (s *Service) GetApproval(id string, now time.Time) (*sharedpaycycle.ApprovalRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.approvals.ApprovalStatus(id, now)
}
