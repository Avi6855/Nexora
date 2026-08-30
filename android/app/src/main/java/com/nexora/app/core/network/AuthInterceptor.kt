package com.nexora.app.core.network

import android.util.Base64
import com.nexora.app.core.security.SecureTokenStorage
import okhttp3.Interceptor
import okhttp3.Response
import org.json.JSONObject
import javax.inject.Inject

class AuthInterceptor @Inject constructor(
    private val tokenStorage: SecureTokenStorage
) : Interceptor {
    override fun intercept(chain: Interceptor.Chain): Response {
        val originalRequest = chain.request()

        val token = tokenStorage.getAccessToken()
        if (token.isNullOrEmpty()) {
            return chain.proceed(originalRequest)
        }

        val builder = originalRequest.newBuilder()
            .header("Authorization", "Bearer $token")
            .header("Content-Type", "application/json")
            .header("Accept", "application/json")

        val userId = extractUserIdFromJwt(token)
        if (userId != null) {
            builder.header("X-User-ID", userId)
        }

        return chain.proceed(builder.build())
    }

    private fun extractUserIdFromJwt(token: String): String? {
        return try {
            val parts = token.split(".")
            if (parts.size < 2) return null
            val payload = parts[1]
            val decoded = Base64.decode(payload, Base64.URL_SAFE or Base64.NO_WRAP or Base64.NO_PADDING)
            val json = JSONObject(String(decoded))
            json.getString("sub")
        } catch (e: Exception) {
            null
        }
    }
}
