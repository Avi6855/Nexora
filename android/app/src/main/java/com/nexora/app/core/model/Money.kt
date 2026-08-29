package com.nexora.app.core.model

import kotlinx.serialization.Serializable

@Serializable
data class Money(
    val amount: Long = 0,
    val currency: String = "GBP"
) {
    fun formatted(): String {
        val major = amount / 100
        val minor = amount % 100
        return when (currency) {
            "GBP" -> "\u00A3${major}.${String.format("%02d", minor)}"
            "USD" -> "$${major}.${String.format("%02d", minor)}"
            "EUR" -> "\u20AC${major}.${String.format("%02d", minor)}"
            else -> "$currency ${major}.${String.format("%02d", minor)}"
        }
    }

    companion object {
        fun zero(currency: String = "GBP") = Money(amount = 0, currency = currency)
    }
}
