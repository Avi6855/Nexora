package com.nexora.app.core.model

import com.google.gson.annotations.SerializedName

/**
 * SafeToSpend is the Financial Intelligence Platform's daily decision:
 * what is left to spend before the account runs past its buffer by payday.
 * Mirrors insights-service domain.SafeToSpend (amounts in minor units).
 */
data class SafeToSpend(
    @SerializedName("account_id") val accountId: String = "",
    @SerializedName("available_now") val availableNow: Long = 0,
    @SerializedName("upcoming_bills") val upcomingBills: Long = 0,
    @SerializedName("forecast_spend_month") val forecastSpendMonth: Long = 0,
    @SerializedName("forecast_daily") val forecastDaily: Long = 0,
    @SerializedName("buffer") val buffer: Long = 0,
    @SerializedName("safe_to_spend") val safeToSpend: Long = 0,
    @SerializedName("safe_to_spend_daily") val safeToSpendDaily: Long = 0,
    @SerializedName("days_to_payday") val daysToPayday: Int = 0,
    @SerializedName("expected_income") val expectedIncome: Long = 0,
    @SerializedName("category_totals") val categoryTotals: List<CategorySpend> = emptyList()
)

/** CategorySpend is one row of the 30-day category breakdown. */
data class CategorySpend(
    @SerializedName("category") val category: String = "",
    @SerializedName("total_30d") val total30d: Long = 0,
    @SerializedName("daily_avg") val dailyAvg: Long = 0
)

/** Subscription is a detected recurring payment (with price-hike flags). */
data class Subscription(
    @SerializedName("account_id") val accountId: String = "",
    @SerializedName("subscription_id") val subscriptionId: String = "",
    @SerializedName("merchant") val merchant: String = "",
    @SerializedName("monthly_amount") val monthlyAmount: Long = 0,
    @SerializedName("last_amount") val lastAmount: Long = 0,
    @SerializedName("previous_amount") val previousAmount: Long = 0,
    @SerializedName("cadence_days") val cadenceDays: Int = 30,
    @SerializedName("transaction_count") val transactionCount: Int = 0,
    @SerializedName("price_hike_pct") val priceHikePct: Double = 0.0,
    @SerializedName("active") val active: Boolean = false,
    @SerializedName("last_seen_at") val lastSeenAt: String = "",
    @SerializedName("next_expected_at") val nextExpectedAt: String = ""
) {
    val hasPriceHike: Boolean get() = priceHikePct >= 10.0
}

/** InsightAlert is one intelligence alert (price hike, anomaly, income...). */
data class InsightAlert(
    @SerializedName("alert_id") val alertId: String = "",
    @SerializedName("alert_type") val alertType: String = "",
    @SerializedName("title") val title: String = "",
    @SerializedName("body") val body: String = "",
    @SerializedName("created_at") val createdAt: String = ""
)

/** SalaryStatus is the income-lifecycle view (growth, payday, lateness). */
data class SalaryStatus(
    @SerializedName("account_id") val accountId: String = "",
    @SerializedName("source") val source: String = "",
    @SerializedName("last_amount") val lastAmount: Long = 0,
    @SerializedName("monthly_avg") val monthlyAvg: Long = 0,
    @SerializedName("last_income_at") val lastIncomeAt: String = "",
    @SerializedName("next_expected_at") val nextExpectedAt: String = "",
    @SerializedName("late_by_days") val lateByDays: Int = 0,
    @SerializedName("last_increase_pct") val lastIncreasePct: Double = 0.0
) {
    val isLate: Boolean get() = lateByDays > 0
    val hasIncrease: Boolean get() = lastIncreasePct >= 1.0
}

/** RunwayResult answers "what if my income stopped tomorrow?". */
data class RunwayResult(
    @SerializedName("account_id") val accountId: String = "",
    @SerializedName("available_now") val availableNow: Long = 0,
    @SerializedName("essentials_monthly") val essentialsMonthly: Long = 0,
    @SerializedName("lifestyle_monthly") val lifestyleMonthly: Long = 0,
    @SerializedName("total_monthly") val totalMonthly: Long = 0,
    @SerializedName("runway_months_total") val runwayMonthsTotal: Double = 0.0,
    @SerializedName("runway_months_essentials") val runwayMonthsEssentials: Double = 0.0,
    @SerializedName("verdict") val verdict: String = "",
    @SerializedName("scenario") val scenario: RunwayScenario? = null
)

/** RunwayScenario is the what-if projection (extra cost over a horizon). */
data class RunwayScenario(
    @SerializedName("extra_monthly_cost") val extraMonthlyCost: Long = 0,
    @SerializedName("horizon_months") val horizonMonths: Int = 0,
    @SerializedName("remaining_after") val remainingAfter: Long = 0,
    @SerializedName("survives") val survives: Boolean = false,
    @SerializedName("summary") val summary: String = ""
)
