package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/insights-service/internal/clients"
	"github.com/nexora/nexora/services/insights-service/internal/domain"
	"github.com/nexora/nexora/services/insights-service/internal/repository"
)

// AlertPublisher pushes an insight alert onto nexora.insights.alerts so the
// notification service fans it out to the app feed + SSE stream.
type AlertPublisher interface {
	PublishAlert(ctx context.Context, alert *domain.InsightAlert) error
}

// IntelligenceService maintains the projections and computes the decisions:
// subscription detection, price-hike detection, income detection and
// safe-to-spend.
type IntelligenceService struct {
	repo         repository.Repository
	ledger       *clients.LedgerClient
	alerts       AlertPublisher
	logger       zerolog.Logger

	// safeToSpendFloorPct of monthly income is kept as buffer in
	// safe-to-spend (Monzo-style "leave a little behind").
	safeToSpendFloorPct float64

	// lastUserPerAccount caches the owning user for alert routing (alerts are
	// keyed by account; the notification pipeline needs a user to route to).
	lastUserMu sync.Mutex
	lastUser   map[uuid.UUID]uuid.UUID
}

// NewIntelligenceService builds the service.
func NewIntelligenceService(repo repository.Repository, ledger *clients.LedgerClient, alerts AlertPublisher, logger zerolog.Logger) *IntelligenceService {
	return &IntelligenceService{
		repo:                repo,
		ledger:              ledger,
		alerts:              alerts,
		logger:              logger,
		safeToSpendFloorPct: 0.05,
		lastUser:            make(map[uuid.UUID]uuid.UUID),
	}
}

// rememberUser caches the owning user of an account for alert routing.
func (s *IntelligenceService) rememberUser(accountID, userID uuid.UUID) {
	if userID == uuid.Nil {
		return
	}
	s.lastUserMu.Lock()
	defer s.lastUserMu.Unlock()
	s.lastUser[accountID] = userID
}

// userFor resolves the cached owning user of an account (uuid.Nil when never
// seen).
func (s *IntelligenceService) userFor(accountID uuid.UUID) uuid.UUID {
	s.lastUserMu.Lock()
	defer s.lastUserMu.Unlock()
	return s.lastUser[accountID]
}

// ── Spend ingestion ─────────────────────────────────────────────────────────

// RecordSpend updates the category baseline projection and runs the
// subscription detector over the incoming spend. It is the entry point for
// card/payment events and is idempotent-tolerant: recomputes converge because
// projections are keyed, not accumulated blindly (amount history per
// subscription is stored, not streamed sums).
func (s *IntelligenceService) RecordSpend(ctx context.Context, ev *domain.SpendEvent) error {
	if err := ev.Validate(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = now
	}
	s.rememberUser(ev.AccountID, ev.UserID)
	category := ev.Category
	if category == "" {
		category = string(categoryOf(ev.Description))
	}

	if err := s.updateBaseline(ctx, ev, category, now); err != nil {
		return fmt.Errorf("updating baseline: %w", err)
	}
	if err := s.detectSubscription(ctx, ev, now); err != nil {
		// Detection failure must not lose the baseline update.
		s.logger.Error().Err(err).Str("description", ev.Description).Msg("subscription detection failed")
	}
	return nil
}

func (s *IntelligenceService) updateBaseline(ctx context.Context, ev *domain.SpendEvent, category string, now time.Time) error {
	// Recompute the 30-day aggregate from the real ledger so repeated events
	// never double-count (the projection is derived, not accumulated).
	entries, err := s.ledger.GetEntries(ctx, ev.AccountID, 500)
	if err != nil {
		return err
	}
	windowStart := now.Add(-30 * 24 * time.Hour)
	totals := map[string]int64{}
	counts := map[string]int{}
	for _, e := range entries {
		if e.EntryType != "DEBIT" {
			continue
		}
		if e.CreatedAt.Before(windowStart) {
			continue
		}
		cat := e.Category
		if cat == "" {
			cat = string(categoryOf(e.Description))
		}
		totals[cat] += e.Amount
		counts[cat]++
	}
	// Ensure the triggering category exists even if the ledger read lags.
	totals[category] += ev.Amount
	counts[category]++

	for cat, total := range totals {
		daily := int64(float64(total) / 30.0)
		if err := s.repo.UpsertBaseline(ctx, &domain.Baseline{
			AccountID: ev.AccountID,
			Category:  cat,
			DailyAvg:  daily,
			Total30d:  total,
			TxCount:   counts[cat],
			UpdatedAt: now,
		}); err != nil {
			return err
		}
	}
	return nil
}

// ── Subscription detection ──────────────────────────────────────────────────

func (s *IntelligenceService) detectSubscription(ctx context.Context, ev *domain.SpendEvent, now time.Time) error {
	key := domain.SubscriptionKey(ev.Description)
	merchant := cleanMerchant(ev.Description)

	existing, err := s.repo.GetSubscription(ctx, ev.AccountID, key)
	if err != nil {
		return err
	}

	if existing == nil {
		// First sighting: record it but do not alert yet. A single payment is
		// not a subscription; the second sighting within cadence range is.
		sub := &domain.Subscription{
			AccountID:      ev.AccountID,
			SubscriptionID: key,
			Merchant:       merchant,
			MonthlyAmount:  monthlyEquivalent(ev.Amount, 30),
			LastAmount:     ev.Amount,
			CadenceDays:    30,
			TxCount:        1,
			Active:         false,
			FirstSeenAt:    ev.OccurredAt,
			LastSeenAt:     ev.OccurredAt,
			NextExpectedAt: ev.OccurredAt.Add(30 * 24 * time.Hour),
		}
		return s.repo.UpsertSubscription(ctx, sub)
	}

	gapDays := int(ev.OccurredAt.Sub(existing.LastSeenAt).Hours() / 24)
	if gapDays < 7 || gapDays > 45 {
		// Outside subscription cadence: not a recurring charge. Keep the row
		// but don't grow confidence.
		return nil
	}

	previous := existing.LastAmount
	sub := &domain.Subscription{
		AccountID:      existing.AccountID,
		SubscriptionID: existing.SubscriptionID,
		Merchant:       existing.Merchant,
		MonthlyAmount:  monthlyEquivalent(ev.Amount, gapDays),
		LastAmount:     ev.Amount,
		PreviousAmount: previous,
		CadenceDays:    existing.CadenceDays,
		TxCount:        existing.TxCount + 1,
		Active:         existing.Active,
		Notified:       existing.Notified,
		FirstSeenAt:    existing.FirstSeenAt,
		LastSeenAt:     ev.OccurredAt,
		NextExpectedAt: ev.OccurredAt.Add(time.Duration(gapDays) * 24 * time.Hour),
	}
	if existing.TxCount <= 2 {
		sub.CadenceDays = (existing.CadenceDays + gapDays) / 2
	}

	// Price-hike detection: >10% increase on an active subscription alerts once.
	hikePct := 0.0
	if previous > 0 && ev.Amount > previous {
		hikePct = float64(ev.Amount-previous) / float64(previous) * 100
	}
	if hikePct >= 10.0 && existing.Active && !existing.Notified {
		sub.Notified = true
		s.emit(ctx, ev.AccountID, domain.AlertTypePriceHike,
			fmt.Sprintf("%s put your price up", merchant),
			fmt.Sprintf("%s increased from %s to %s (%.0f%% more than usual). Review it if you no longer need it.",
				merchant, domain.Pounds(previous), domain.Pounds(ev.Amount), hikePct))
	}

	wasActive := existing.Active
	sub.Active = existing.TxCount+1 >= 2
	if sub.Active && !wasActive {
		s.emit(ctx, ev.AccountID, domain.AlertTypeNewSubscription,
			fmt.Sprintf("Subscription detected: %s", merchant),
			fmt.Sprintf("We spotted a recurring %s payment (about every %d days). Annualised it costs about %s.",
				merchant, sub.CadenceDays, domain.Pounds(sub.MonthlyAmount*12)))
	}

	return s.repo.UpsertSubscription(ctx, sub)
}

// ── Income detection ────────────────────────────────────────────────────────

// RecordIncome stores a detected recurring income credit (salary/payroll
// style) and alerts the first time it is seen.
func (s *IntelligenceService) RecordIncome(ctx context.Context, ev *domain.SpendEvent) error {
	now := time.Now().UTC()
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = now
	}
	s.rememberUser(ev.AccountID, ev.UserID)

	existing, err := s.repo.GetIncome(ctx, ev.AccountID)
	if err != nil {
		return err
	}

	in := &domain.Income{
		AccountID:    ev.AccountID,
		UserID:       ev.UserID,
		Source:       cleanMerchant(ev.Description),
		LastAmount:   ev.Amount,
		MonthlyAvg:   ev.Amount,
		LastIncomeAt: ev.OccurredAt,
		UpdatedAt:    now,
	}
	if existing != nil {
		gapDays := int(ev.OccurredAt.Sub(existing.LastIncomeAt).Hours() / 24)
		if gapDays > 0 && gapDays <= 45 {
			// Simple exponential blend towards the new amount keeps the
			// average stable across one-off variations.
			blend := 0.3
			monthly := monthlyEquivalent(ev.Amount, gapDays)
			in.MonthlyAvg = int64(float64(existing.MonthlyAvg)*(1-blend) + float64(monthly)*blend)
		} else {
			in.MonthlyAvg = existing.MonthlyAvg
		}
	}

	if existing == nil {
		s.emit(ctx, ev.AccountID, domain.AlertTypeIncomeDetected,
			"Regular income detected",
			fmt.Sprintf("We noticed %s from %s. Safe-to-spend will use this to plan your month.",
				domain.Pounds(ev.Amount), in.Source))
	}

	return s.repo.UpsertIncome(ctx, in)
}

// ── Safe-to-spend ───────────────────────────────────────────────────────────

// GetSafeToSpend computes the daily/total safe-to-spend decision over real
// balances, the active subscriptions due before payday, forecast spend from
// baselines, and detected income.
func (s *IntelligenceService) GetSafeToSpend(ctx context.Context, accountID uuid.UUID) (*domain.SafeToSpend, error) {
	now := time.Now().UTC()

	balance, err := s.ledger.GetBalance(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("reading balance: %w", err)
	}

	baselines, err := s.repo.ListBaselines(ctx, accountID)
	if err != nil {
		return nil, err
	}
	subs, err := s.repo.ListSubscriptions(ctx, accountID)
	if err != nil {
		return nil, err
	}
	income, err := s.repo.GetIncome(ctx, accountID)
	if err != nil {
		return nil, err
	}

	// Forecast daily/monthly spend from the 30-day baselines.
	var forecastDaily int64
	cats := make([]domain.CategorySpend, 0, len(baselines))
	for _, b := range baselines {
		forecastDaily += b.DailyAvg
		cats = append(cats, domain.CategorySpend{Category: b.Category, Total30d: b.Total30d, DailyAvg: b.DailyAvg})
	}
	sort.Slice(cats, func(i, j int) bool { return cats[i].Total30d > cats[j].Total30d })

	forecastMonth := forecastDaily * 30

	// Upcoming bills: active subscriptions due before the next payday.
	var daysToPayday int
	var expectedIncome int64
	var expectedPayday *time.Time
	if income != nil && income.MonthlyAvg > 0 {
		expectedIncome = income.MonthlyAvg
		// Salary typically repeats monthly; project the next date after
		// last_income_at plus a month, stepping forward if already past.
		next := income.LastIncomeAt.AddDate(0, 1, 0)
		for next.Before(now) {
			next = next.AddDate(0, 1, 0)
		}
		daysToPayday = int(next.Sub(now).Hours() / 24) + 1
		expectedPayday = &next
	}
	if daysToPayday <= 0 {
		daysToPayday = 30
	}

	upcoming := int64(0)
	for _, sub := range subs {
		if !sub.Active {
			continue
		}
		next := sub.NextExpectedAt
		for next.Before(now) && sub.CadenceDays > 0 {
			next = next.AddDate(0, 0, sub.CadenceDays)
		}
		if expectedPayday != nil && next.Before(*expectedPayday) {
			upcoming += sub.LastAmount
		}
	}

	buffer := int64(float64(expectedIncome) * s.safeToSpendFloorPct)

	// Total safe-to-spend until payday: available money minus committed bills,
	// minus forecast spend for the remaining days, minus the safety buffer.
	safeTotal := balance.Available - upcoming - forecastDaily*int64(daysToPayday) - buffer
	safeDaily := int64(0)
	if daysToPayday > 0 {
		safeDaily = safeTotal / int64(daysToPayday)
	}
	if safeTotal < 0 {
		safeTotal = 0
	}
	if safeDaily < 0 {
		safeDaily = 0
	}

	return &domain.SafeToSpend{
		AccountID:        accountID,
		AvailableNow:     balance.Available,
		UpcomingBills:    upcoming,
		ForecastSpend:    forecastMonth,
		ForecastDaily:    forecastDaily,
		Buffer:           buffer,
		SafeToSpend:      safeTotal,
		SafeToSpendDaily: safeDaily,
		DaysToPayday:     daysToPayday,
		ExpectedPayday:   expectedPayday,
		ExpectedIncome:   expectedIncome,
		CategoryTotals:   cats,
	}, nil
}

// GetSubscriptions returns the active detected subscriptions (and their
// price-hike flags) for the account.
func (s *IntelligenceService) GetSubscriptions(ctx context.Context, accountID uuid.UUID) ([]*domain.Subscription, error) {
	subs, err := s.repo.ListSubscriptions(ctx, accountID)
	if err != nil {
		return nil, err
	}
	active := make([]*domain.Subscription, 0, len(subs))
	for _, sub := range subs {
		if sub.Active {
			active = append(active, sub)
		}
	}
	return active, nil
}

// GetAlerts lists recent insight alerts for the account.
func (s *IntelligenceService) GetAlerts(ctx context.Context, accountID uuid.UUID, limit int) ([]*domain.InsightAlert, error) {
	return s.repo.ListAlerts(ctx, accountID, limit)
}

// ── Income lifecycle intelligence (salary switch) ───────────────────────────

// GetSalaryStatus reports the income lifecycle: growth, lateness, and the
// next expected payday, derived from the detected income history.
func (s *IntelligenceService) GetSalaryStatus(ctx context.Context, accountID uuid.UUID) (*domain.SalaryStatus, error) {
	income, err := s.repo.GetIncome(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if income == nil || income.MonthlyAvg <= 0 {
		return nil, nil // no salary detected yet
	}

	now := time.Now().UTC()
	next := income.LastIncomeAt.AddDate(0, 1, 0)
	for next.Before(now) {
		next = next.AddDate(0, 1, 0)
	}
	lateBy := 0
	if now.After(income.LastIncomeAt.AddDate(0, 1, 0)) {
		lateBy = int(now.Sub(income.LastIncomeAt.AddDate(0, 1, 0)).Hours() / 24)
	}

	// Increase % compares the latest salary against the blended average.
	increasePct := 0.0
	if income.MonthlyAvg > 0 && income.LastAmount > income.MonthlyAvg {
		increasePct = float64(income.LastAmount-income.MonthlyAvg) / float64(income.MonthlyAvg) * 100
	}

	return &domain.SalaryStatus{
		AccountID:       accountID,
		Source:          income.Source,
		LastAmount:      income.LastAmount,
		MonthlyAvg:      income.MonthlyAvg,
		LastIncomeAt:    income.LastIncomeAt,
		NextExpectedAt:  next,
		LateByDays:      lateBy,
		LastIncreasePct: increasePct,
	}, nil
}

// CheckSalaryLate runs periodically (ticker): if the expected payday has
// passed by more than 2 days, emit a one-shot SALARY_LATE alert for the
// current lateness window (notified once per late period via Notified flag).
func (s *IntelligenceService) CheckSalaryLate(ctx context.Context) {
	// The projection table is keyed by account; we scan per known account by
	// asking the repository for accounts that have income rows.
	accounts, err := s.repo.ListAccountsWithIncome(ctx)
	if err != nil {
		s.logger.Error().Err(err).Msg("salary-late scan failed")
		return
	}
	now := time.Now().UTC()
	for _, accountID := range accounts {
		income, err := s.repo.GetIncome(ctx, accountID)
		if err != nil || income == nil || income.MonthlyAvg <= 0 {
			continue
		}
		due := income.LastIncomeAt.AddDate(0, 1, 0)
		lateBy := int(now.Sub(due).Hours() / 24)
		if lateBy < 2 {
			continue // within normal variability
		}
		// One alert per late period: skip if we already alerted for this due
		// date (store the marker in the alerts feed by checking recent ones).
		recent, err := s.repo.ListAlerts(ctx, accountID, 20)
		if err == nil {
			already := false
			for _, a := range recent {
				if a.AlertType == domain.AlertTypeSalaryLate && a.CreatedAt.After(due) {
					already = true
					break
				}
			}
			if already {
				continue
			}
		}
		s.emit(ctx, accountID, domain.AlertTypeSalaryLate,
			"Your salary hasn't arrived",
			fmt.Sprintf("Your usual payment from %s was expected around %s and hasn't landed yet (%d days late). Check with your employer if this looks wrong.",
				income.Source, due.Format("2 Jan"), lateBy))
	}
}

// ── Runway / what-if simulator ──────────────────────────────────────────────

// GetRunway answers "what if my income stopped tomorrow?" using the real
// balance, the category baselines (essentials vs lifestyle) and an optional
// what-if scenario (extra monthly cost over a horizon).
func (s *IntelligenceService) GetRunway(ctx context.Context, accountID uuid.UUID, extraMonthly int64, horizonMonths int) (*domain.RunwayResult, error) {
	balance, err := s.ledger.GetBalance(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("reading balance: %w", err)
	}
	baselines, err := s.repo.ListBaselines(ctx, accountID)
	if err != nil {
		return nil, err
	}

	var essentials, lifestyle int64
	for _, b := range baselines {
		monthly := b.DailyAvg * 30
		if domain.IsEssentialCategory(b.Category) {
			essentials += monthly
		} else {
			lifestyle += monthly
		}
	}
	total := essentials + lifestyle

	result := &domain.RunwayResult{
		AccountID:         accountID,
		AvailableNow:      balance.Available,
		EssentialsMonthly: essentials,
		LifestyleMonthly:  lifestyle,
		TotalMonthly:      total,
	}
	if total > 0 {
		result.RunwayMonthsTotal = float64(balance.Available) / float64(total)
	}
	if essentials > 0 {
		result.RunwayMonthsEssentl = float64(balance.Available) / float64(essentials)
	}
	if total > 0 {
		depletion := time.Now().UTC().AddDate(0, 0, int(result.RunwayMonthsTotal*30))
		result.DepletionDate = &depletion
	}
	switch {
	case total <= 0:
		result.Verdict = "No spending history yet — runway can't be estimated."
	case result.RunwayMonthsTotal >= 6:
		result.Verdict = "Strong position: over 6 months of coverage even with zero income."
	case result.RunwayMonthsTotal >= 3:
		result.Verdict = "Healthy: about 3–6 months of coverage if income stopped."
	case result.RunwayMonthsTotal >= 1:
		result.Verdict = "Tight: about a month of coverage — worth building a buffer."
	default:
		result.Verdict = "At risk: less than a month of coverage if income stopped."
	}

	if extraMonthly > 0 || horizonMonths > 0 {
		h := horizonMonths
		if h <= 0 {
			h = 1
		}
		monthly := total + extraMonthly
		remaining := balance.Available - monthly*int64(h)
		result.Scenario = &domain.RunwayScenario{
			ExtraMonthlyCost: extraMonthly,
			HorizonMonths:    h,
			RemainingAfter:   remaining,
			Survives:         remaining >= 0,
		}
		if remaining >= 0 {
			result.Scenario.Summary = fmt.Sprintf("You'd still hold %s after %d months with the extra %s/month.",
				domain.Pounds(remaining), h, domain.Pounds(extraMonthly))
		} else {
			result.Scenario.Summary = fmt.Sprintf("You'd run out about %s short after %d months with the extra %s/month.",
				domain.Pounds(-remaining), h, domain.Pounds(extraMonthly))
		}
	}
	return result, nil
}

// ── Anomaly detection (Push #2 seam) ────────────────────────────────────────

// CheckAnomaly flags a spend that is far outside the account's own baseline
// for its category. Returns true when an anomaly alert was emitted. It is a
// pure function over stored projections, so it can also be replayed.
func (s *IntelligenceService) CheckAnomaly(ctx context.Context, ev *domain.SpendEvent) (bool, error) {
	baselines, err := s.repo.ListBaselines(ctx, ev.AccountID)
	if err != nil {
		return false, err
	}
	category := ev.Category
	if category == "" {
		category = string(categoryOf(ev.Description))
	}
	for _, b := range baselines {
		if !strings.EqualFold(b.Category, category) || b.TxCount < 3 {
			continue
		}
		// A single transaction is an anomaly when it alone exceeds 10× the
		// category's average transaction size.
		avgTx := b.Total30d / int64(maxInt(1, b.TxCount))
		if avgTx > 0 && ev.Amount > 10*avgTx {
			s.emit(ctx, ev.AccountID, domain.AlertTypeSpendingAnomaly,
				"Unusual spending detected",
				fmt.Sprintf("A %s payment of %s is much higher than your usual %s spending.",
					strings.ToLower(strings.ReplaceAll(category, "_", " ")),
					domain.Pounds(ev.Amount),
					strings.ToLower(strings.ReplaceAll(category, "_", " "))))
			return true, nil
		}
	}
	return false, nil
}

// emit persists an alert and publishes it to the notification pipeline.
func (s *IntelligenceService) emit(ctx context.Context, accountID uuid.UUID, alertType, title, body string) {
	alert := &domain.InsightAlert{
		AccountID: accountID,
		AlertID:   uuid.New(),
		AlertType: alertType,
		Title:     title,
		Body:      body,
		CreatedAt: time.Now().UTC(),
	}
	if payload, err := json.Marshal(map[string]interface{}{
		"account_id": accountID.String(),
		"alert_type": alertType,
		"user_id":    s.userFor(accountID).String(),
	}); err == nil {
		alert.Payload = string(payload)
	}
	if err := s.repo.InsertAlert(ctx, alert); err != nil {
		s.logger.Error().Err(err).Str("alert_type", alertType).Msg("failed to persist insight alert")
	}
	if s.alerts != nil {
		if err := s.alerts.PublishAlert(ctx, alert); err != nil {
			s.logger.Error().Err(err).Str("alert_type", alertType).Msg("failed to publish insight alert")
		}
	}
	s.logger.Info().Str("alert_type", alertType).Str("account_id", accountID.String()).Msg("insight alert emitted")
}

// ── helpers ─────────────────────────────────────────────────────────────────

func monthlyEquivalent(amount int64, gapDays int) int64 {
	if gapDays <= 0 {
		gapDays = 30
	}
	return int64(float64(amount) * 30.0 / float64(gapDays))
}

func cleanMerchant(description string) string {
	d := strings.TrimSpace(description)
	if len(d) > 60 {
		d = d[:60]
	}
	return d
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// categoryOf is a minimal local category fallback for entries that arrive
// without one; the ledger infers categories at booking, so this only handles
// legacy rows.
type Category string

func categoryOf(description string) Category {
	d := strings.ToLower(description)
	switch {
	case strings.Contains(d, "salary"), strings.Contains(d, "payroll"):
		return "INCOME"
	case strings.Contains(d, "netflix"), strings.Contains(d, "spotify"):
		return "ENTERTAINMENT"
	case strings.Contains(d, "tesco"), strings.Contains(d, "sainsbury"):
		return "GROCERIES"
	default:
		return "OTHER"
	}
}

var _ = domain.Pounds
