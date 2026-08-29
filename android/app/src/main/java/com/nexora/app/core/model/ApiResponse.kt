package com.nexora.app.core.model

import kotlinx.serialization.Serializable

@Serializable
data class ApiResponse<T>(
    val status: String = "success",
    val data: T? = null,
    val message: String? = null,
    val error: String? = null
) {
    val isSuccess: Boolean get() = status == "success" && error == null
}
