package com.nexora.app.core.model

import com.google.gson.annotations.SerializedName

data class Account(
    @SerializedName("account_id") val accountId: String = "",
    @SerializedName("user_id") val userId: String = "",
    @SerializedName("account_type") val accountType: String = "CURRENT",
    @SerializedName("currency") val currency: String = "GBP",
    @SerializedName("available_balance") val availableBalanceMoney: Money? = null,
    @SerializedName("current_balance") val currentBalanceMoney: Money? = null,
    @SerializedName("reserved_balance") val reservedBalanceMoney: Money? = null,
    @SerializedName("status") val status: String = "active",
    @SerializedName("created_at") val createdAt: String = "",
    @SerializedName("updated_at") val updatedAt: String = "",
    // Legacy/UI fields - keep for backward compat with existing UI code
    val name: String = "",
    val accountNumber: String = "",
    val sortCode: String = ""
) {
    // Compatibility getters used by HomeViewModel & UI
    val id: String get() = accountId
    val type: String get() = accountType.lowercase()
    val balance: Long get() = currentBalanceMoney?.amount ?: 0L
    val availableBalance: Long get() = availableBalanceMoney?.amount ?: 0L
    val reservedBalance: Long get() = reservedBalanceMoney?.amount ?: 0L
    val pendingBalance: Long get() = 0L

    fun balanceMoney() = currentBalanceMoney ?: Money(amount = balance, currency = currency)
    fun availableMoney() = availableBalanceMoney ?: Money(amount = availableBalance, currency = currency)
    fun pendingMoney() = Money(amount = pendingBalance, currency = currency)
    fun reservedMoney() = reservedBalanceMoney ?: Money(amount = reservedBalance, currency = currency)
}
