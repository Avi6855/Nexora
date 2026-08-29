package com.nexora.app.core.network.api

import com.nexora.app.core.model.ApiResponse
import com.nexora.app.core.model.AuthTokens
import kotlinx.serialization.Serializable
import kotlinx.serialization.SerialName
import retrofit2.http.Body
import retrofit2.http.POST

@Serializable
data class LoginRequest(
    val email: String,
    val password: String,
    @SerialName("device_id")
    val deviceId: String = ""
)

@Serializable
data class RegisterRequest(
    val email: String,
    val password: String,
    @SerialName("first_name")
    val firstName: String,
    @SerialName("last_name")
    val lastName: String,
    @SerialName("phone_number")
    val phoneNumber: String = "",
    @SerialName("device_id")
    val deviceId: String = ""
)

@Serializable
data class OtpRequest(
    val email: String,
    val otp: String,
    @SerialName("device_id")
    val deviceId: String = ""
)

@Serializable
data class RefreshRequest(
    @SerialName("refresh_token")
    val refreshToken: String
)

@Serializable
data class LogoutRequest(
    @SerialName("device_id")
    val deviceId: String = ""
)

@Serializable
data class DeviceRequest(
    @SerialName("device_id")
    val deviceId: String,
    @SerialName("device_name")
    val deviceName: String,
    @SerialName("device_type")
    val deviceType: String = "android"
)

@Serializable
data class ResendOtpRequest(
    val email: String
)

interface AuthApi {
    @POST("v1/auth/login")
    suspend fun login(@Body request: LoginRequest): ApiResponse<AuthTokens>

    @POST("v1/auth/register")
    suspend fun register(@Body request: RegisterRequest): ApiResponse<AuthTokens>

    @POST("v1/auth/verify-otp")
    suspend fun verifyOtp(@Body request: OtpRequest): ApiResponse<AuthTokens>

    @POST("v1/auth/refresh")
    suspend fun refreshToken(@Body request: RefreshRequest): ApiResponse<AuthTokens>

    @POST("v1/auth/logout")
    suspend fun logout(@Body request: LogoutRequest): ApiResponse<Unit>

    @POST("v1/auth/device/register")
    suspend fun registerDevice(@Body request: DeviceRequest): ApiResponse<Unit>

    @POST("v1/auth/resend-otp")
    suspend fun resendOtp(@Body request: ResendOtpRequest): ApiResponse<Unit>
}
