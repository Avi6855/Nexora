package com.nexora.app.core.network.api

import com.nexora.app.core.model.ApiResponse
import com.nexora.app.core.model.User
import retrofit2.http.GET
import retrofit2.http.PUT
import retrofit2.http.Body
import kotlinx.serialization.Serializable

@Serializable
data class UpdateProfileRequest(
    val firstName: String,
    val lastName: String,
    val phoneNumber: String
)

interface UserApi {
    @GET("v1/users/me")
    suspend fun getCurrentUser(): ApiResponse<User>

    @PUT("v1/users/me")
    suspend fun updateProfile(@Body request: UpdateProfileRequest): ApiResponse<User>
}
