package com.nexora.app.core.network.api

import com.nexora.app.core.model.ApiResponse
import com.nexora.app.core.model.Pot
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path

@Serializable
data class CreatePotRequest(
    val name: String,
    val goal: Long = 0,
    val currency: String = "GBP",
    val icon: String = "savings",
    val color: String = "#00BFA5"
)

@Serializable
data class DepositRequest(
    val amount: Long
)

interface PotApi {
    @GET("v1/pots")
    suspend fun getPots(): ApiResponse<List<Pot>>

    @GET("v1/pots/{potId}")
    suspend fun getPot(@Path("potId") potId: String): ApiResponse<Pot>

    @POST("v1/pots")
    suspend fun createPot(@Body request: CreatePotRequest): ApiResponse<Pot>

    @PUT("v1/pots/{potId}/deposit")
    suspend fun depositToPot(
        @Path("potId") potId: String,
        @Body request: DepositRequest
    ): ApiResponse<Pot>

    @PUT("v1/pots/{potId}/withdraw")
    suspend fun withdrawFromPot(
        @Path("potId") potId: String,
        @Body request: DepositRequest
    ): ApiResponse<Pot>
}
