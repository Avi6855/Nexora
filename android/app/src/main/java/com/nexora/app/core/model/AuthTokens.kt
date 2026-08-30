package com.nexora.app.core.model

import com.google.gson.annotations.SerializedName

data class AuthTokens(
    @SerializedName("access_token") val accessToken: String = "",
    @SerializedName("refresh_token") val refreshToken: String = "",
    @SerializedName("expires_at") val expiresAt: Long = 0,
    val tokenType: String = "Bearer"
)
