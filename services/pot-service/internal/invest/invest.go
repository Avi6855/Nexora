package invest

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	sharedinvest "github.com/nexora/nexora/shared/investments"
)

// AnnualAllowanceMinor is £20,000 expressed in minor units.
const AnnualAllowanceMinor = int64(2000000)

var (
	ErrGoalNotFound   = errors.New("joint goal not found")
	ErrEngineNotFound = errors.New("tax-lot engine not found")
	ErrAlreadyExists  = errors.New("already exists")
)

// Service holds ISA control, joint goals, tax-lot engines and holdings.
type Service struct {
	mu       sync.RWMutex
	tower    *sharedinvest.ControlTower
	goals    map[string]*sharedinvest.JointGoal
	engines  map[string]*sharedinvest.TaxLotEngine
	holdings map[string]sharedinvest.Holding
}

func NewService() *Service {
	return &Service{
		tower:    sharedinvest.NewControlTower(AnnualAllowanceMinor),
		goals:    map[string]*sharedinvest.JointGoal{},
		engines:  map[string]*sharedinvest.TaxLotEngine{},
		holdings: map[string]sharedinvest.Holding{},
	}
}

// ContributeISA admits one contribution against the annual allowance.
func (s *Service) ContributeISA(c sharedinvest.Contribution) error {
	if c.ID == "" {
		return errors.New("contribution id is required")
	}
	if c.OwnerID == "" {
		return errors.New("owner_id is required")
	}
	if c.At.IsZero() {
		c.At = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tower.Admit(c)
}

// RemainingAllowance reports unused allowance for owner+tax year.
// Empty taxYear defaults to the current UK tax year.
func (s *Service) RemainingAllowance(owner, taxYear string) (int64, error) {
	if owner == "" {
		return 0, errors.New("owner is required")
	}
	if taxYear == "" {
		taxYear = sharedinvest.TaxYear(time.Now())
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tower.Remaining(owner, taxYear), nil
}

// CreateJointGoal creates a two-owner household goal.
func (s *Service) CreateJointGoal(id, name string, targetMinor int64, owners []string) (*sharedinvest.JointGoal, error) {
	if id == "" {
		id = uuid.NewString()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.goals[id]; ok {
		return nil, fmt.Errorf("joint goal %s: %w", id, ErrAlreadyExists)
	}
	g, err := sharedinvest.NewJointGoal(id, name, targetMinor, owners)
	if err != nil {
		return nil, err
	}
	s.goals[id] = g
	return g, nil
}

// ContributeJointGoal adds one owner's money toward the shared target.
func (s *Service) ContributeJointGoal(goalID, ownerID string, amountMinor int64) (*sharedinvest.JointGoal, error) {
	if amountMinor <= 0 {
		return nil, errors.New("amount_minor must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.goals[goalID]
	if !ok {
		return nil, ErrGoalNotFound
	}
	if err := g.Contribute(ownerID, amountMinor); err != nil {
		return nil, err
	}
	return g, nil
}

// OpenTaxLots builds (or replaces) the per-symbol lot engine.
func (s *Service) OpenTaxLots(symbol string, method sharedinvest.LotMethod, lots []sharedinvest.Lot) error {
	if symbol == "" {
		return errors.New("symbol is required")
	}
	if len(lots) == 0 {
		return errors.New("at least one lot is required")
	}
	for _, l := range lots {
		if l.Symbol != symbol {
			return fmt.Errorf("lot %s does not match symbol %s", l.ID, symbol)
		}
		if l.UnitsMilli <= 0 {
			return fmt.Errorf("lot %s units must be positive", l.ID)
		}
	}
	if method == "" {
		method = sharedinvest.LotFIFO
	}
	switch method {
	case sharedinvest.LotFIFO, sharedinvest.LotLIFO, sharedinvest.LotHIFO:
	default:
		return fmt.Errorf("unknown lot method %s", method)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.engines[symbol] = sharedinvest.NewTaxLotEngine(method, lots)
	return nil
}

// SellLots consumes lots in method order.
func (s *Service) SellLots(symbol string, unitsMilli, pricePerUnitMilli int64) (*sharedinvest.SellResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.engines[symbol]
	if !ok {
		return nil, ErrEngineNotFound
	}
	return e.Sell(symbol, unitsMilli, pricePerUnitMilli)
}

// ApplyAction rewrites a holding for one corporate action and stores it.
func (s *Service) ApplyAction(h sharedinvest.Holding, a sharedinvest.CorporateAction) (*sharedinvest.Holding, *sharedinvest.HoldingActionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, res, err := sharedinvest.ApplyCorporateAction(h, a)
	if err != nil {
		return nil, nil, err
	}
	if out.Symbol != h.Symbol {
		delete(s.holdings, h.Symbol)
	}
	s.holdings[out.Symbol] = *out
	return out, res, nil
}
