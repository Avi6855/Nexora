package com.nexora.app.core.model

import kotlinx.serialization.Serializable

@Serializable
data class Account(
    val id: String = "",
    val name: String = "",
    val type: String = "current",
    val currency: String = "GBP",
    val balance: Long = 0,
    val availableBalance: Long = 0,
    val pendingBalance: Long = 0,
    val reservedBalance: Long = 0,
    val accountNumber: String = "",
    val sortCode: String = "",
    val status: String = "active",
    val createdAt: String = "",
    val updatedAt: String = ""
) {
    fun balanceMoney() = Money(amount = balance, currency = currency)
    fun availableMoney() = Money(amount = availableBalance, currency = currency)
    fun pendingMoney() = Money(amount = pendingBalance, currency = currency)
    fun reservedMoney() = Money(amount = reservedBalance, currency = currency)
}
