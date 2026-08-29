package com.nexora.app.core.model

import kotlinx.serialization.Serializable

@Serializable
data class User(
    val id: String = "",
    val email: String = "",
    val firstName: String = "",
    val lastName: String = "",
    val phoneNumber: String = "",
    val profileImageUrl: String? = null,
    val createdAt: String = "",
    val updatedAt: String = ""
) {
    val fullName: String get() = "$firstName $lastName".trim()
    val initials: String get() {
        val first = firstName.firstOrNull()?.uppercase() ?: ""
        val last = lastName.firstOrNull()?.uppercase() ?: ""
        return "$first$last"
    }
}
