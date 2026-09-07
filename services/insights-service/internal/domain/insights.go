package domain

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Subscription is a detected recurring payment. The detector keys on a
// deterministic subscriptionID (hash of the normalised merchant name), so the
// same Netflix payment always lands on the same row and cadence/amount history
// accumulates across recomputes.
type Subscription struct {
	AccountID      uuid.UUID `json:"account_id"`
	SubscriptionID string    `json:"subscription_id"`
	Merchant       string    `json:"merchant"`
	MonthlyAmount  int64     `json:"monthly_amount"`
	LastAmount     int64     `json:"last_amount"`
	PreviousAmount int64     `json:"previous_amount"`
	CadenceDays    int       `json:"cadence_days"`
	TxCount        int       `json:"transaction_count"`
	PriceHikePct   float64   `json:"price_hike_pct"`
	Active         bool      `json:"active"`
	Notified       bool      `json:"notified"`
	FirstSeenAt    time.Time `json:"first_seen_at"`
	LastSeenAt     time.Time `json:"last_seen_at"`
	NextExpectedAt time.Time `json:"next_expected_at"`
}

// Income is a detected recurring income (salary/payroll style credit).
type Income struct {
	AccountID    uuid.UUID `json:"account_id"`
	UserID       uuid.UUID `json:"user_id"`
	Source       string    `json:"source"`
	LastAmount   int64     `json:"last_amount"`
	MonthlyAvg   int64     `json:"monthly_avg"`
	LastIncomeAt time.Time `json:"last_income_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Baseline is the per-category spend projection maintained on every spend
// event: a 30-day rolling daily average and total.
type Baseline struct {
	AccountID uuid.UUID `json:"account_id"`
	Category  string    `json:"category"`
	DailyAvg  int64     `json:"daily_avg"`
	Total30d  int64     `json:"total_30d"`
	TxCount   int       `json:"tx_count"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SafeToSpend is the daily decision the app surfaces on Home: what is left to
// spend today before the account runs past its buffer before payday.
type SafeToSpend struct {
	AccountID       uuid.UUID        `json:"account_id"`
	AvailableNow    int64            `json:"available_now"`
	UpcomingBills   int64            `json:"upcoming_bills"`
	ForecastSpend   int64            `json:"forecast_spend_month"`
	ForecastDaily   int64            `json:"forecast_daily"`
	Buffer          int64            `json:"buffer"`
	SafeToSpend     int64            `json:"safe_to_spend"`
	SafeToSpendDaily int64           `json:"safe_to_spend_daily"`
	DaysToPayday    int              `json:"days_to_payday"`
	ExpectedPayday  *time.Time       `json:"expected_payday,omitempty"`
	ExpectedIncome  int64            `json:"expected_income"`
	CategoryTotals  []CategorySpend  `json:"category_totals"`
}

// CategorySpend is one row of the category breakdown in SafeToSpend.
type CategorySpend struct {
	Category string `json:"category"`
	Total30d int64  `json:"total_30d"`
	DailyAvg int64  `json:"daily_avg"`
}

// InsightAlert is an emitted intelligence event (new subscription, price
// hike, income detected, anomaly). Persisted for the Insights feed and
// published to nexora.insights.alerts for the notification/SSE pipeline.
type InsightAlert struct {
	AccountID uuid.UUID    `json:"account_id"`
	AlertID   uuid.UUID    `json:"alert_id"`
	AlertType string       `json:"alert_type"`
	Title     string       `json:"title"`
	Body      string       `json:"body"`
	Payload   string       `json:"payload,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
}

const (
	AlertTypeNewSubscription = "NEW_SUBSCRIPTION"
	AlertTypePriceHike       = "PRICE_HIKE"
	AlertTypeIncomeDetected  = "INCOME_DETECTED"
	AlertTypeSpendingAnomaly = "SPENDING_ANOMALY"
	AlertTypeSalaryIncrease  = "SALARY_INCREASE"
	AlertTypeSalaryLate      = "SALARY_LATE"
)

// SalaryStatus is the income-lifecycle view: is the salary on track, late, or
// has it grown — computed from detected income history.
type SalaryStatus struct {
	AccountID       uuid.UUID `json:"account_id"`
	Source          string    `json:"source"`
	LastAmount      int64     `json:"last_amount"`
	MonthlyAvg      int64     `json:"monthly_avg"`
	LastIncomeAt    time.Time `json:"last_income_at"`
	NextExpectedAt  time.Time `json:"next_expected_at"`
	LateByDays      int       `json:"late_by_days"`
	LastIncreasePct float64   `json:"last_increase_pct"`
}

// RunwayResult answers "what if my income stopped tomorrow?" — how long the
// current balance covers essential and total spending, plus a scenario
// projection (rent rise, new commitment, lost salary).
type RunwayResult struct {
	AccountID           uuid.UUID       `json:"account_id"`
	AvailableNow        int64           `json:"available_now"`
	EssentialsMonthly   int64           `json:"essentials_monthly"`
	LifestyleMonthly    int64           `json:"lifestyle_monthly"`
	TotalMonthly        int64           `json:"total_monthly"`
	RunwayMonthsTotal   float64         `json:"runway_months_total"`
	RunwayMonthsEssentl float64         `json:"runway_months_essentials"`
	DepletionDate       *time.Time      `json:"depletion_date,omitempty"`
	Verdict             string          `json:"verdict"`
	Scenario            *RunwayScenario `json:"scenario,omitempty"`
}

// RunwayScenario projects a what-if: extra monthly cost and/or a horizon.
type RunwayScenario struct {
	ExtraMonthlyCost int64 `json:"extra_monthly_cost"`
	HorizonMonths    int   `json:"horizon_months"`
	// RemainingAfter is available − (total monthly + extra) × horizon.
	RemainingAfter int64  `json:"remaining_after"`
	Survives       bool   `json:"survives"`
	Summary        string `json:"summary"`
}

// Essential categories used for the essentials-only runway. Everything else
// is lifestyle spend (can be cut when money is tight).
var essentialCategories = map[string]bool{
	"GROCERIES": true, "BILLS": true, "HOUSING": true, "UTILITIES": true,
	"TRANSPORT": true, "COUNCIL_TAX": true, "RENT": true, "ENERGY": true,
}

// IsEssentialCategory reports whether a category counts as essential spend.
func IsEssentialCategory(c string) bool { return essentialCategories[strings.ToUpper(c)] }

// Validate checks basic sanity for a spend event used to update baselines.
func (e *SpendEvent) Validate() error {
	if e.AccountID == uuid.Nil {
		return fmt.Errorf("account_id is required")
	}
	if e.Amount <= 0 {
		return fmt.Errorf("amount must be positive")
	}
	return nil
}

// SpendEvent is the minimal projection input for a debit booked on an
// account. The insights consumer derives it from card/payment events.
type SpendEvent struct {
	AccountID   uuid.UUID
	UserID      uuid.UUID
	Amount      int64
	Currency    string
	Description string
	Category    string
	OccurredAt  time.Time
}

// normaliseKey canonicalises a merchant description so that "NETFLIX.COM",
// "Netflix UK" and "netflix" collapse onto one subscription.
func normaliseKey(merchant string) string {
	s := strings.ToLower(strings.TrimSpace(merchant))
	s = strings.NewReplacer(".", "", ",", "", " ltd", "", " limited", "", " uk", "", " com", "").Replace(s)
	return s
}

// SubscriptionKey derives the deterministic subscription id from a merchant
// description (stable across restarts and Kafka replays).
func SubscriptionKey(merchant string) string {
	return fmt.Sprintf("sub:%s", normaliseKey(merchant))
}

// Pounds formats minor units for human-readable alert text.
func Pounds(minor int64) string {
	return fmt.Sprintf("£%.2f", float64(minor)/100.0)
}

// clamp01 bounds a probability to [0,1].
func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }
