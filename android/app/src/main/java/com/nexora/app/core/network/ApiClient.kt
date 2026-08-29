package com.nexora.app.core.network

import okhttp3.OkHttpClient
import okhttp3.logging.HttpLoggingInterceptor
import retrofit2.Retrofit
import java.util.concurrent.TimeUnit
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class ApiClient @Inject constructor(
    private val okHttpClient: OkHttpClient,
    private val retrofit: Retrofit
) {
    fun <T> create(serviceClass: Class<T>): T = retrofit.create(serviceClass)
}
