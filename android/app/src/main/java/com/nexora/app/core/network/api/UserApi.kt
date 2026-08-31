package com.nexora.app.core.network.api

import com.nexora.app.core.model.User
import retrofit2.http.GET
import retrofit2.http.PUT
import retrofit2.http.Body
import com.google.gson.annotations.SerializedName

data class UpdateProfileRequest(
    @SerializedName("first_name") val firstName: String,
    @SerializedName("last_name") val lastName: String,
    @SerializedName("phone") val phoneNumber: String
)

interface UserApi {
    @GET("v1/users/me")
    suspend fun getCurrentUser(): User

    @PUT("v1/users/me")
    suspend fun updateProfile(@Body request: UpdateProfileRequest): User
}
