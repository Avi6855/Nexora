package com.nexora.app.core.network.api

import com.nexora.app.core.model.Pot
import com.google.gson.annotations.SerializedName
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path

data class CreatePotRequest(
    @SerializedName("name") val name: String,
    @SerializedName("target_amount") val goal: Long = 0,
    @SerializedName("currency") val currency: String = "GBP",
    val icon: String = "savings",
    val color: String = "#00BFA5"
)

data class DepositRequest(
    @SerializedName("amount") val amount: Long
)

interface PotApi {
    @GET("v1/pots")
    suspend fun getPots(): List<Pot>

    @GET("v1/pots/{potId}")
    suspend fun getPot(@Path("potId") potId: String): Pot

    @POST("v1/pots")
    suspend fun createPot(@Body request: CreatePotRequest): Pot

    @PUT("v1/pots/{potId}/deposit")
    suspend fun depositToPot(
        @Path("potId") potId: String,
        @Body request: DepositRequest
    ): Pot

    @PUT("v1/pots/{potId}/withdraw")
    suspend fun withdrawFromPot(
        @Path("potId") potId: String,
        @Body request: DepositRequest
    ): Pot

    @PUT("v1/pots/{potId}/roundup")
    suspend fun setRoundUp(
        @Path("potId") potId: String,
        @Body request: RoundUpRequest
    ): Pot
}

data class RoundUpRequest(val enabled: Boolean)
