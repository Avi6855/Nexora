package testgrid

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Journey is an ordered-step definition crossed with a fault matrix.
type Journey struct {
	ID           string            `json:"id"`
	Steps        []string          `json:"steps"`
	Faults       []string          `json:"faults"`
	Expectations map[string]string `json:"expectations,omitempty"` // key step\x00fault -> expected outcome
	CreatedAt    time.Time         `json:"created_at"`
}

// CellResult is one step×fault outcome.
type CellResult struct {
	Step     string `json:"step"`
	Fault    string `json:"fault"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Pass     bool   `json:"pass"`
}

// GridResult is one full matrix run.
type GridResult struct {
	JourneyID string       `json:"journey_id"`
	At        time.Time    `json:"at"`
	Cells     []CellResult `json:"cells"`
	Passed    int          `json:"passed"`
	Failed    int          `json:"failed"`
}

// GridStore holds journeys and their latest results.
type GridStore struct {
	mu       sync.RWMutex
	journeys map[string]*Journey
	results  map[string]*GridResult
	logger   zerolog.Logger
}

// NewGridStore returns an empty store.
func NewGridStore(logger zerolog.Logger) *GridStore {
	return &GridStore{journeys: make(map[string]*Journey), results: make(map[string]*GridResult), logger: logger}
}

func journeyCellKey(step, fault string) string { return step + "\x00" + fault }

// actualFor is the deterministic system-under-test stub: each fault maps
// to a visible outcome; "none" always succeeds.
func actualFor(step, fault string) string {
	switch strings.ToLower(strings.TrimSpace(fault)) {
	case "", "none":
		return "success"
	case "dependency_timeout":
		return "retry"
	case "duplicate_event":
		return "deduped"
	case "partial_write":
		return "compensated"
	case "slow_db":
		return "degraded"
	case "message_reorder":
		return "resequenced"
	default:
		return "failed:" + fault
	}
}

// CreateJourney stores a journey definition.
func (s *GridStore) CreateJourney(steps, faults []string, expectations map[string]string, now time.Time) (*Journey, error) {
	if len(steps) == 0 {
		return nil, fmt.Errorf("%w: at least one step is required", ErrGridInvalidInput)
	}
	if len(faults) == 0 {
		return nil, fmt.Errorf("%w: at least one fault is required", ErrGridInvalidInput)
	}
	for i, st := range steps {
		if strings.TrimSpace(st) == "" {
			return nil, fmt.Errorf("%w: step[%d] is empty", ErrGridInvalidInput, i)
		}
	}
	for i, f := range faults {
		if strings.TrimSpace(f) == "" {
			return nil, fmt.Errorf("%w: fault[%d] is empty", ErrGridInvalidInput, i)
		}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	j := &Journey{
		ID:        "jrny-" + uuid.NewString(),
		Steps:     append([]string(nil), steps...),
		Faults:    append([]string(nil), faults...),
		CreatedAt: now,
	}
	if len(expectations) > 0 {
		j.Expectations = make(map[string]string, len(expectations))
		for k, v := range expectations {
			j.Expectations[k] = v
		}
	}
	s.mu.Lock()
	s.journeys[j.ID] = j
	s.mu.Unlock()
	s.logger.Info().Str("journey_id", j.ID).Int("steps", len(steps)).Int("faults", len(faults)).Msg("journey created")
	cp := *j
	cp.Steps = append([]string(nil), j.Steps...)
	cp.Faults = append([]string(nil), j.Faults...)
	return &cp, nil
}

// GetJourney fetches one definition.
func (s *GridStore) GetJourney(id string) (*Journey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.journeys[id]
	if !ok {
		return nil, ErrJourneyNotFound
	}
	cp := *j
	cp.Steps = append([]string(nil), j.Steps...)
	cp.Faults = append([]string(nil), j.Faults...)
	exp := make(map[string]string, len(j.Expectations))
	for k, v := range j.Expectations {
		exp[k] = v
	}
	cp.Expectations = exp
	return &cp, nil
}

// RunGrid executes steps×faults, comparing actual vs expected per cell.
func (s *GridStore) RunGrid(id string, now time.Time) (*GridResult, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.journeys[id]
	if !ok {
		return nil, ErrJourneyNotFound
	}
	res := &GridResult{JourneyID: id, At: now}
	for _, step := range j.Steps {
		for _, fault := range j.Faults {
			actual := actualFor(step, fault)
			expected, has := j.Expectations[journeyCellKey(step, fault)]
			if !has {
				expected = actual // unspecified cells default to the stub
			}
			cell := CellResult{Step: step, Fault: fault, Expected: expected, Actual: actual, Pass: expected == actual}
			res.Cells = append(res.Cells, cell)
			if cell.Pass {
				res.Passed++
			} else {
				res.Failed++
			}
		}
	}
	s.results[id] = res
	s.logger.Info().Str("journey_id", id).Int("passed", res.Passed).Int("failed", res.Failed).Msg("journey grid run")
	cp := *res
	cp.Cells = append([]CellResult(nil), res.Cells...)
	return &cp, nil
}

// Results returns the latest grid run for a journey.
func (s *GridStore) Results(id string) (*GridResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.journeys[id]; !ok {
		return nil, ErrJourneyNotFound
	}
	r, ok := s.results[id]
	if !ok {
		return nil, ErrJourneyNotRun
	}
	cp := *r
	cp.Cells = append([]CellResult(nil), r.Cells...)
	return &cp, nil
}
