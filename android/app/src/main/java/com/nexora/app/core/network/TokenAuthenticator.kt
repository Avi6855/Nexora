package com.nexora.app.core.network

import com.nexora.app.core.security.SecureTokenStorage
import kotlinx.coroutines.runBlocking
import okhttp3.Authenticator
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okhttp3.Route
import okhttp3.MediaType.Companion.toMediaType
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class TokenAuthenticator @Inject constructor(
    private val tokenStorage: SecureTokenStorage,
    private val okHttpClient: dagger.Lazy<OkHttpClient>
) : Authenticator {

    override fun authenticate(route: Route?, response: Response): Request? {
        if (response.code == 401) {
            val refreshToken = tokenStorage.getRefreshToken()
            if (refreshToken.isNullOrEmpty()) {
                tokenStorage.clearTokens()
                return null
            }

            val refreshRequest = Request.Builder()
                .url("http://10.0.2.2:8000/api/v1/auth/refresh")
                .post(
                    """{"refresh_token":"$refreshToken"}"""
                        .toRequestBody("application/json".toMediaType())
                )
                .build()

            val refreshResponse = try {
                okHttpClient.get().newCall(refreshRequest).execute()
            } catch (e: Exception) {
                tokenStorage.clearTokens()
                return null
            }

            if (refreshResponse.isSuccessful) {
                val body = refreshResponse.body?.string()
                val newAccessToken = extractToken(body, "access_token")
                val newRefreshToken = extractToken(body, "refresh_token")

                if (newAccessToken != null) {
                    tokenStorage.saveTokens(newAccessToken, newRefreshToken ?: refreshToken)
                    return Request.Builder()
                        .url(response.request.url)
                        .header("Authorization", "Bearer $newAccessToken")
                        .header("Content-Type", "application/json")
                        .header("Accept", "application/json")
                        .build()
                }
            }

            tokenStorage.clearTokens()
        }
        return null
    }

    private fun extractToken(json: String?, key: String): String? {
        if (json == null) return null
        val pattern = "\"$key\"\\s*:\\s*\"([^\"]+)\"".toRegex()
        return pattern.find(json)?.groupValues?.get(1)
    }
}
