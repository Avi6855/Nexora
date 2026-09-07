package com.nexora.app.core.network.api

import com.nexora.app.core.model.Transaction
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.PUT
import retrofit2.http.Path
import retrofit2.http.Query

interface TransferApi {
    @GET("v1/ledger/accounts/{accountId}/entries")
    suspend fun getTransactions(
        @Path("accountId") accountId: String,
        @Query("limit") limit: Int = 50,
        @Query("offset") offset: Int = 0
    ): List<Transaction>

    @GET("v1/ledger/accounts/{accountId}/entries")
    suspend fun searchTransactions(
        @Path("accountId") accountId: String,
        @Query("query") query: String,
        @Query("category") category: String? = null,
        @Query("type") type: String? = null,
        @Query("limit") limit: Int = 50
    ): List<Transaction>

    @GET("v1/ledger/transactions/{transactionId}")
    suspend fun getTransaction(
        @Path("transactionId") transactionId: String
    ): Transaction

    @PUT("v1/ledger/entries/{entryId}/note")
    suspend fun updateNote(
        @Path("entryId") entryId: String,
        @Body request: NoteRequest
    ): Map<String, String>
}

data class NoteRequest(val note: String)
