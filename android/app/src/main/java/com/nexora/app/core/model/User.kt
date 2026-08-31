package com.nexora.app.core.model

import com.google.gson.annotations.SerializedName

data class User(
    @SerializedName("user_id") val id: String = "",
    @SerializedName("email") val email: String = "",
    @SerializedName("first_name") val firstName: String = "",
    @SerializedName("last_name") val lastName: String = "",
    @SerializedName("phone") val phoneNumber: String = "",
    val profileImageUrl: String? = null,
    @SerializedName("created_at") val createdAt: String = "",
    @SerializedName("updated_at") val updatedAt: String = "",
    @SerializedName("status") val status: String = ""
) {
    val fullName: String get() = "$firstName $lastName".trim()
    val initials: String get() {
        val first = firstName.firstOrNull()?.uppercase() ?: ""
        val last = lastName.firstOrNull()?.uppercase() ?: ""
        return "$first$last"
    }
}
