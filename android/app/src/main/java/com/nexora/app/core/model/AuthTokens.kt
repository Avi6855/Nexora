package com.nexora.app.core.model

import kotlinx.serialization.Serializable

@Serializable
data class AuthTokens(
    val accessToken: String = "",
    val refreshToken: String = "",
    val expiresIn: Long = 0,
    val tokenType: String = "Bearer"
)
