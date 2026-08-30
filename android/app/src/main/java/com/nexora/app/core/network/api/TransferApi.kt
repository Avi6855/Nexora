package com.nexora.app.core.network.api

import com.nexora.app.core.model.ApiResponse
import com.nexora.app.core.model.Transaction
import retrofit2.http.GET
import retrofit2.http.Path
import retrofit2.http.Query

interface TransferApi {
    @GET("v1/accounts/{accountId}/transactions")
    suspend fun getTransactions(
        @Path("accountId") accountId: String,
        @Query("limit") limit: Int = 50,
        @Query("offset") offset: Int = 0
    ): ApiResponse<List<Transaction>>

    @GET("v1/transfers/{transactionId}")
    suspend fun getTransaction(
        @Path("transactionId") transactionId: String
    ): ApiResponse<Transaction>
}
