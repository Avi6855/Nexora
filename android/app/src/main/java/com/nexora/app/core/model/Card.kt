package com.nexora.app.core.model

import kotlinx.serialization.Serializable

@Serializable
data class Card(
    val id: String = "",
    val accountId: String = "",
    val type: String = "debit",
    val lastFourDigits: String = "",
    val expiryMonth: Int = 0,
    val expiryYear: Int = 0,
    val status: String = "active",
    val spendLimit: Long = 0,
    val dailyLimit: Long = 0,
    val currency: String = "GBP",
    val isFrozen: Boolean = false,
    val createdAt: String = "",
    val updatedAt: String = ""
) {
    val formattedExpiry: String get() = String.format("%02d/%02d", expiryMonth, expiryYear % 100)
}
