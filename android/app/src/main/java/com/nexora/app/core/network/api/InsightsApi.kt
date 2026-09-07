package com.nexora.app.core.network.api

import com.nexora.app.core.model.InsightAlert
import com.nexora.app.core.model.RunwayResult
import com.nexora.app.core.model.SalaryStatus
import com.nexora.app.core.model.SafeToSpend
import com.nexora.app.core.model.Subscription
import retrofit2.http.GET
import retrofit2.http.Query

/**
 * InsightsApi talks to the Financial Intelligence Platform (insights-service,
 * routed through Envoy at /v1/insights).
 */
interface InsightsApi {

    @GET("v1/insights/safe-to-spend")
    suspend fun getSafeToSpend(
        @Query("account_id") accountId: String
    ): SafeToSpend

    @GET("v1/insights/subscriptions")
    suspend fun getSubscriptions(
        @Query("account_id") accountId: String
    ): List<Subscription>

    @GET("v1/insights/alerts")
    suspend fun getAlerts(
        @Query("account_id") accountId: String,
        @Query("limit") limit: Int = 50
    ): List<InsightAlert>

    @GET("v1/insights/salary/status")
    suspend fun getSalaryStatus(@Query("account_id") accountId: String): SalaryStatus

    @GET("v1/insights/runway")
    suspend fun getRunway(
        @Query("account_id") accountId: String,
        @Query("extra_monthly") extraMonthly: Long = 0,
        @Query("horizon_months") horizonMonths: Int = 0
    ): RunwayResult
}
