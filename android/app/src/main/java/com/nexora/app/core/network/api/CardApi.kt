package com.nexora.app.core.network.api

import com.google.gson.annotations.SerializedName
import com.nexora.app.core.model.Card
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path

interface CardApi {
    @GET("v1/cards")
    suspend fun getCards(): List<Card>

    @GET("v1/cards/{cardId}")
    suspend fun getCard(@Path("cardId") cardId: String): Card

    @POST("v1/cards")
    suspend fun requestCard(): Card

    @PUT("v1/cards/{cardId}/freeze")
    suspend fun freezeCard(@Path("cardId") cardId: String): Card

    @PUT("v1/cards/{cardId}/unfreeze")
    suspend fun unfreezeCard(@Path("cardId") cardId: String): Card

    @PUT("v1/cards/{cardId}/limits")
    suspend fun updateLimits(
        @Path("cardId") cardId: String,
        @Body request: UpdateLimitsRequest
    ): Map<String, String>

    @PUT("v1/cards/{cardId}/controls")
    suspend fun updateControls(
        @Path("cardId") cardId: String,
        @Body request: UpdateControlsRequest
    ): Card
}

data class UpdateLimitsRequest(
    @SerializedName("daily_limit") val dailyLimit: Long,
    @SerializedName("monthly_limit") val monthlyLimit: Long
)

/** All fields optional; only the provided ones are changed server-side. */
data class UpdateControlsRequest(
    @SerializedName("online_enabled") val onlineEnabled: Boolean? = null,
    @SerializedName("atm_enabled") val atmEnabled: Boolean? = null,
    @SerializedName("gambling_block_enabled") val gamblingBlockEnabled: Boolean? = null
)
