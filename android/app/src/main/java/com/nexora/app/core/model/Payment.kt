package com.nexora.app.core.model

import kotlinx.serialization.Serializable

enum class PaymentState {
    CREATED,
    AUTHORIZED,
    PROCESSING,
    UNKNOWN,
    CONFIRMED,
    SETTLED,
    FAILED,
    REVERSED,
    CANCELLED
}

@Serializable
data class Payment(
    val id: String = "",
    val accountId: String = "",
    val type: String = "transfer",
    val state: PaymentState = PaymentState.CREATED,
    val amount: Long = 0,
    val currency: String = "GBP",
    val recipientName: String = "",
    val recipientAccountNumber: String = "",
    val recipientSortCode: String = "",
    val reference: String = "",
    val description: String = "",
    val fee: Long = 0,
    val createdAt: String = "",
    val updatedAt: String = ""
) {
    fun amountMoney() = Money(amount = amount, currency = currency)
    fun feeMoney() = Money(amount = fee, currency = currency)
}
