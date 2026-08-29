package com.nexora.app.core.network.api

import com.nexora.app.core.model.ApiResponse
import com.nexora.app.core.model.Card
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path

interface CardApi {
    @GET("v1/cards")
    suspend fun getCards(): ApiResponse<List<Card>>

    @GET("v1/cards/{cardId}")
    suspend fun getCard(@Path("cardId") cardId: String): ApiResponse<Card>

    @POST("v1/cards")
    suspend fun requestCard(): ApiResponse<Card>

    @PUT("v1/cards/{cardId}/freeze")
    suspend fun freezeCard(@Path("cardId") cardId: String): ApiResponse<Card>

    @PUT("v1/cards/{cardId}/unfreeze")
    suspend fun unfreezeCard(@Path("cardId") cardId: String): ApiResponse<Card>
}
