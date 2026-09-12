// Package resilience extends insights-service with what-if resilience
// scenarios:
//
//   - named scenarios {job_loss, rent_hike, income_gap_months} computed
//     from runway inputs (available balance + essential/total monthly
//     spend) into survival months per scenario plus a coverage verdict.
package resilience

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

var (
	// ErrNotFound surfaces unknown scenarios.
	ErrNotFound = errors.New("not found")
	// ErrInvalid surfaces bad scenario params or runway inputs.
	ErrInvalid = errors.New("invalid request")
)

// Scenario is one named what-if: job loss and/or a rent hike projected over
// an income-gap horizon.
type Scenario struct {
	ID              string    `json:"id"`
	AccountID       string    `json:"account_id"`
	Name            string    `json:"name"`
	JobLoss         bool      `json:"job_loss"`
	RentHikeMonthly int64     `json:"rent_hike_monthly"`
	IncomeGapMonths int       `json:"income_gap_months"`
	CreatedAt       time.Time `json:"created_at"`
}

// RunwayInputs are the live runway figures a scenario run is computed from.
type RunwayInputs struct {
	AvailableNow      int64 `json:"available_now"`
	EssentialsMonthly int64 `json:"essentials_monthly"`
	TotalMonthly      int64 `json:"total_monthly"`
}

// ScenarioOutcome is the survival verdict for one scenario run.
type ScenarioOutcome struct {
	ScenarioID             string  `json:"scenario_id"`
	Name                   string  `json:"name"`
	AdjustedMonthly        int64   `json:"adjusted_monthly"`
	SurvivalMonthsTotal    float64 `json:"survival_months_total"`
	SurvivalMonthsEssentls float64 `json:"survival_months_essentials"`
	CoversGap              bool    `json:"covers_gap"`
	Verdict                string  `json:"verdict"`
}

// Service stores scenarios and runs them against runway inputs.
type Service struct {
	mu        sync.RWMutex
	scenarios map[string]*Scenario
	logger    zerolog.Logger
}

// NewService builds an empty scenario service.
func NewService(logger zerolog.Logger) *Service {
	return &Service{scenarios: map[string]*Scenario{}, logger: logger}
}

// CreateScenario stores one named what-if scenario.
func (s *Service) CreateScenario(accountID, name string, jobLoss bool, rentHikeMonthly int64, incomeGapMonths int) (*Scenario, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("%w: account_id is required", ErrInvalid)
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if rentHikeMonthly < 0 {
		return nil, fmt.Errorf("%w: rent_hike_monthly must be >= 0", ErrInvalid)
	}
	if incomeGapMonths < 0 {
		return nil, fmt.Errorf("%w: income_gap_months must be >= 0", ErrInvalid)
	}
	sc := &Scenario{
		ID:              uuid.NewString(),
		AccountID:       accountID,
		Name:            strings.TrimSpace(name),
		JobLoss:         jobLoss,
		RentHikeMonthly: rentHikeMonthly,
		IncomeGapMonths: incomeGapMonths,
		CreatedAt:       time.Now().UTC(),
	}
	s.mu.Lock()
	s.scenarios[sc.ID] = sc
	s.mu.Unlock()
	s.logger.Info().Str("scenario_id", sc.ID).Bool("job_loss", jobLoss).Msg("resilience scenario created")
	out := *sc
	return &out, nil
}

// ListScenarios returns scenarios, optionally filtered by account.
func (s *Service) ListScenarios(accountID string) []Scenario {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Scenario
	for _, sc := range s.scenarios {
		if accountID != "" && sc.AccountID != accountID {
			continue
		}
		out = append(out, *sc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if out == nil {
		out = []Scenario{}
	}
	return out
}

// RunScenario computes survival months for one scenario from runway inputs.
func (s *Service) RunScenario(id string, in RunwayInputs) (*ScenarioOutcome, error) {
	if in.AvailableNow < 0 || in.EssentialsMonthly < 0 || in.TotalMonthly < 0 {
		return nil, fmt.Errorf("%w: runway inputs must be >= 0", ErrInvalid)
	}
	s.mu.RLock()
	sc, ok := s.scenarios[strings.TrimSpace(id)]
	var cp Scenario
	if ok {
		cp = *sc
	}
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("scenario %q: %w", id, ErrNotFound)
	}
	adjustedTotal := in.TotalMonthly + cp.RentHikeMonthly
	adjustedEssentials := in.EssentialsMonthly + cp.RentHikeMonthly
	out := &ScenarioOutcome{ScenarioID: cp.ID, Name: cp.Name, AdjustedMonthly: adjustedTotal}
	if adjustedTotal > 0 {
		out.SurvivalMonthsTotal = float64(in.AvailableNow) / float64(adjustedTotal)
	}
	if adjustedEssentials > 0 {
		out.SurvivalMonthsEssentls = float64(in.AvailableNow) / float64(adjustedEssentials)
	}
	if cp.IncomeGapMonths > 0 {
		out.CoversGap = out.SurvivalMonthsTotal >= float64(cp.IncomeGapMonths)
	} else {
		out.CoversGap = out.SurvivalMonthsTotal >= 3
	}
	prefix := ""
	if cp.JobLoss {
		prefix = "Job-loss: "
	}
	switch {
	case adjustedTotal <= 0:
		out.Verdict = prefix + "No spending history yet — resilience can't be estimated."
	case out.SurvivalMonthsTotal >= 6:
		out.Verdict = prefix + "Strong position: over 6 months of coverage."
	case out.SurvivalMonthsTotal >= 3:
		out.Verdict = prefix + "Healthy: about 3–6 months of coverage."
	case out.SurvivalMonthsTotal >= 1:
		out.Verdict = prefix + "Tight: about a month of coverage — worth building a buffer."
	default:
		out.Verdict = prefix + "At risk: less than a month of coverage."
	}
	if cp.IncomeGapMonths > 0 {
		if out.CoversGap {
			out.Verdict += fmt.Sprintf(" Covers the %d-month income gap.", cp.IncomeGapMonths)
		} else {
			out.Verdict += fmt.Sprintf(" Does not cover the %d-month income gap.", cp.IncomeGapMonths)
		}
	}
	s.logger.Info().Str("scenario_id", cp.ID).Float64("survival_months", out.SurvivalMonthsTotal).Bool("covers_gap", out.CoversGap).Msg("resilience scenario run")
	return out, nil
}
