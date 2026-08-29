package com.nexora.app.core.model

import kotlinx.serialization.Serializable

@Serializable
data class Transaction(
    val id: String = "",
    val accountId: String = "",
    val type: String = "debit",
    val amount: Long = 0,
    val currency: String = "GBP",
    val description: String = "",
    val category: String = "",
    val merchantName: String? = null,
    val merchantLogoUrl: String? = null,
    val balanceAfter: Long = 0,
    val status: String = "completed",
    val createdAt: String = "",
    val updatedAt: String = ""
) {
    fun amountMoney() = Money(amount = amount, currency = currency)
    fun balanceAfterMoney() = Money(amount = balanceAfter, currency = currency)
    val isCredit: Boolean get() = type == "credit"
    val isDebit: Boolean get() = type == "debit"
}
