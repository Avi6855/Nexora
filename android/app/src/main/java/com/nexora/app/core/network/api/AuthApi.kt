package com.nexora.app.core.network.api

import com.google.gson.annotations.SerializedName
import com.nexora.app.core.model.AuthTokens
import retrofit2.http.Body
import retrofit2.http.POST

data class LoginRequest(
    val email: String,
    val password: String,
    @SerializedName("device_id") val deviceId: String = ""
)

data class RegisterRequest(
    val email: String,
    val password: String,
    @SerializedName("first_name") val firstName: String,
    @SerializedName("last_name") val lastName: String,
    @SerializedName("phone_number") val phoneNumber: String = "",
    @SerializedName("device_id") val deviceId: String = ""
)

data class OtpRequest(
    val email: String,
    val otp: String,
    @SerializedName("device_id") val deviceId: String = ""
)

data class RefreshRequest(
    @SerializedName("refresh_token") val refreshToken: String
)

data class LogoutRequest(
    @SerializedName("device_id") val deviceId: String = ""
)

data class DeviceRequest(
    @SerializedName("device_id") val deviceId: String,
    @SerializedName("device_name") val deviceName: String,
    @SerializedName("device_type") val deviceType: String = "android"
)

data class ResendOtpRequest(
    val email: String
)

interface AuthApi {
    @POST("v1/auth/login")
    suspend fun login(@Body request: LoginRequest): AuthTokens

    @POST("v1/auth/register")
    suspend fun register(@Body request: RegisterRequest): AuthTokens

    @POST("v1/auth/verify-otp")
    suspend fun verifyOtp(@Body request: OtpRequest): AuthTokens

    @POST("v1/auth/refresh")
    suspend fun refreshToken(@Body request: RefreshRequest): AuthTokens

    @POST("v1/auth/logout")
    suspend fun logout(@Body request: LogoutRequest)

    @POST("v1/auth/device/register")
    suspend fun registerDevice(@Body request: DeviceRequest)

    @POST("v1/auth/resend-otp")
    suspend fun resendOtp(@Body request: ResendOtpRequest)
}
