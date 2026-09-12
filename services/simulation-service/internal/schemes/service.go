package schemes

import (
	"time"

	"github.com/rs/zerolog"

	sharedschemes "github.com/nexora/nexora/shared/schemes"
)

// Service is the simulation-service's in-memory scheme store backed by
// shared/schemes.
type Service struct {
	cert     *sharedschemes.CertHarness
	router   *sharedschemes.Router
	calendar *sharedschemes.SettlementCalendar
	ledger   *sharedschemes.FeeLedger
	logger   zerolog.Logger
}

// NewService creates a scheme service pre-seeded with common rails and
// settlement windows.
func NewService(logger zerolog.Logger) *Service {
	s := &Service{
		cert:     sharedschemes.NewCertHarness(logger),
		router:   sharedschemes.NewRouter(logger),
		calendar: sharedschemes.NewSettlementCalendar(logger),
		ledger:   sharedschemes.NewFeeLedger(logger),
		logger:   logger,
	}
	// Default rails (replaceable via AddRail).
	for _, rail := range []sharedschemes.Rail{
		{Name: "FPS", CostBps: 8, P50LatencyMs: 300, Availability: 0.999, Currencies: []string{"GBP"}, AmountMinMinor: 1, AmountMaxMinor: 1000000, Destinations: []string{"UK"}},
		{Name: "VISA", CostBps: 20, P50LatencyMs: 800, Availability: 0.999, Currencies: []string{"GBP", "EUR", "USD"}, AmountMinMinor: 1, AmountMaxMinor: 5000000, Destinations: []string{"UK", "US", "FR"}},
		{Name: "SWIFT", CostBps: 45, P50LatencyMs: 2500, Availability: 0.99, Currencies: []string{"GBP", "USD", "EUR"}, AmountMinMinor: 100, AmountMaxMinor: 10000000, Destinations: []string{"UK", "US", "FR"}},
	} {
		_, _ = s.router.AddRail(rail)
	}
	for _, w := range []sharedschemes.SchemeWindow{
		{Scheme: "VISA", CycleDays: 1, Cutoff: "15:00"},
		{Scheme: "MC", CycleDays: 1, Cutoff: "15:00"},
		{Scheme: "FPS", CycleDays: 0, Cutoff: "15:00"},
	} {
		_, _ = s.calendar.AddScheme(w)
	}
	return s
}

// CertHarness exposes the harness (tests, ops tooling).
func (s *Service) CertHarness() *sharedschemes.CertHarness { return s.cert }

// Router exposes the rail router.
func (s *Service) Router() *sharedschemes.Router { return s.router }

// Calendar exposes the settlement calendar.
func (s *Service) Calendar() *sharedschemes.SettlementCalendar { return s.calendar }

// Ledger exposes the fee ledger.
func (s *Service) Ledger() *sharedschemes.FeeLedger { return s.ledger }

// CertCaseInput is one case to register before a run.
type CertCaseInput struct {
	Kind     sharedschemes.ScenarioKind `json:"kind"`
	Message  map[string]string          `json:"message"`
	Expected map[string]string          `json:"expected"`
}

// RunCert registers any supplied cases then runs the full suite.
func (s *Service) RunCert(cases []CertCaseInput) (*sharedschemes.CertReport, error) {
	for _, c := range cases {
		if _, err := s.cert.RegisterCase(c.Kind, c.Message, c.Expected); err != nil {
			return nil, err
		}
	}
	rep, err := s.cert.RunAll(time.Now().UTC())
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("run_id", rep.RunID).Int("passed", rep.Passed).Int("failed", rep.Failed).Msg("scheme cert run")
	return rep, nil
}

// GetCertRun fetches one report.
func (s *Service) GetCertRun(id string) (*sharedschemes.CertReport, error) {
	return s.cert.GetRun(id)
}

// AddRail registers a rail.
func (s *Service) AddRail(rail sharedschemes.Rail) (*sharedschemes.Rail, error) {
	r, err := s.router.AddRail(rail)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("rail", rail.Name).Msg("scheme rail added")
	return r, nil
}

// Route ranks rails for a payment.
func (s *Service) Route(amountMinor int64, currency, dest string) ([]sharedschemes.RouteOption, error) {
	return s.router.Route(amountMinor, currency, dest)
}

// AddScheme registers a settlement window.
func (s *Service) AddScheme(w sharedschemes.SchemeWindow) (*sharedschemes.SchemeWindow, error) {
	sw, err := s.calendar.AddScheme(w)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("scheme", w.Scheme).Msg("settlement scheme added")
	return sw, nil
}

// NextSettlement resolves the next settlement datetime.
func (s *Service) NextSettlement(scheme string, ts time.Time) (time.Time, error) {
	return s.calendar.NextSettlement(scheme, ts)
}

// AttributeFee stores one fee breakdown.
func (s *Service) AttributeFee(b sharedschemes.FeeBreakdown) (*sharedschemes.FeeTotal, error) {
	tot, err := s.ledger.Attribute(b, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("tx_id", b.TxID).Msg("scheme fee attributed")
	return tot, nil
}

// FeeSummary aggregates one scheme.
func (s *Service) FeeSummary(scheme string) sharedschemes.FeeSummary {
	return s.ledger.Summary(scheme)
}

// FeeSummaryAll aggregates every scheme.
func (s *Service) FeeSummaryAll() []sharedschemes.FeeSummary {
	return s.ledger.SummaryAll()
}
