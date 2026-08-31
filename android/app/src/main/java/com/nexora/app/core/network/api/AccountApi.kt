package com.nexora.app.core.network.api

import com.nexora.app.core.model.Account
import retrofit2.http.GET
import retrofit2.http.Path

interface AccountApi {
    @GET("v1/accounts")
    suspend fun getAccounts(): List<Account>

    @GET("v1/accounts/{accountId}")
    suspend fun getAccount(@Path("accountId") accountId: String): Account

    @GET("v1/accounts/{accountId}/balance")
    suspend fun getBalance(@Path("accountId") accountId: String): Map<String, Any>
}
