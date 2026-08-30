package com.nexora.app.core.di

import com.nexora.app.core.network.ApiClient
import com.nexora.app.core.network.AuthInterceptor
import com.nexora.app.core.network.GsonConverterFactory
import com.nexora.app.core.network.TokenAuthenticator
import com.nexora.app.core.network.api.AccountApi
import com.nexora.app.core.network.api.AuthApi
import com.nexora.app.core.network.api.CardApi
import com.nexora.app.core.network.api.NotificationApi
import com.nexora.app.core.network.api.PaymentApi
import com.nexora.app.core.network.api.PotApi
import com.nexora.app.core.network.api.TransferApi
import com.nexora.app.core.network.api.UserApi
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import okhttp3.OkHttpClient
import okhttp3.logging.HttpLoggingInterceptor
import retrofit2.Retrofit
import java.util.concurrent.TimeUnit
import javax.inject.Singleton

@Module
@InstallIn(SingletonComponent::class)
object AppModule {

    @Provides
    @Singleton
    fun provideOkHttpClient(
        authInterceptor: AuthInterceptor,
        tokenAuthenticator: TokenAuthenticator
    ): OkHttpClient {
        val loggingInterceptor = HttpLoggingInterceptor().apply {
            level = HttpLoggingInterceptor.Level.BODY
        }
        return OkHttpClient.Builder()
            .addInterceptor(authInterceptor)
            .addInterceptor(loggingInterceptor)
            .authenticator(tokenAuthenticator)
            .connectTimeout(30, TimeUnit.SECONDS)
            .readTimeout(30, TimeUnit.SECONDS)
            .writeTimeout(30, TimeUnit.SECONDS)
            .build()
    }

    @Provides
    @Singleton
    fun provideRetrofit(okHttpClient: OkHttpClient): Retrofit {
        return Retrofit.Builder()
            .baseUrl("http://10.0.2.2:8000/")
            .client(okHttpClient)
            .addConverterFactory(GsonConverterFactory.create())
            .build()
    }

    @Provides
    @Singleton
    fun provideApiClient(okHttpClient: OkHttpClient, retrofit: Retrofit): ApiClient {
        return ApiClient(okHttpClient, retrofit)
    }

    @Provides
    @Singleton
    fun provideAuthApi(retrofit: Retrofit): AuthApi = retrofit.create(AuthApi::class.java)

    @Provides
    @Singleton
    fun provideUserApi(retrofit: Retrofit): UserApi = retrofit.create(UserApi::class.java)

    @Provides
    @Singleton
    fun provideAccountApi(retrofit: Retrofit): AccountApi = retrofit.create(AccountApi::class.java)

    @Provides
    @Singleton
    fun providePaymentApi(retrofit: Retrofit): PaymentApi = retrofit.create(PaymentApi::class.java)

    @Provides
    @Singleton
    fun provideTransferApi(retrofit: Retrofit): TransferApi = retrofit.create(TransferApi::class.java)

    @Provides
    @Singleton
    fun provideCardApi(retrofit: Retrofit): CardApi = retrofit.create(CardApi::class.java)

    @Provides
    @Singleton
    fun providePotApi(retrofit: Retrofit): PotApi = retrofit.create(PotApi::class.java)

    @Provides
    @Singleton
    fun provideNotificationApi(retrofit: Retrofit): NotificationApi = retrofit.create(NotificationApi::class.java)
}
