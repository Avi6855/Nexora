package com.nexora.app.core.model

import kotlinx.serialization.Serializable

@Serializable
data class Notification(
    val id: String = "",
    val type: String = "info",
    val title: String = "",
    val message: String = "",
    val read: Boolean = false,
    val actionUrl: String? = null,
    val createdAt: String = ""
) {
    val isUnread: Boolean get() = !read
}
