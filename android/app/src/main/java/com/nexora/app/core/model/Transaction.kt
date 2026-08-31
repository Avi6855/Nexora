package com.nexora.app.core.model

import com.google.gson.annotations.SerializedName

data class Transaction(
    @SerializedName("entry_id") val id: String = "",
    @SerializedName("transaction_id") val transactionId: String = "",
    @SerializedName("account_id") val accountId: String = "",
    @SerializedName("entry_type") val type: String = "debit",
    @SerializedName("entry_direction") val direction: String = "debit",
    @SerializedName("amount") val amount: Long = 0,
    @SerializedName("currency") val currency: String = "GBP",
    @SerializedName("description") val description: String = "",
    val category: String = "",
    @SerializedName("correlation_id") val merchantName: String? = null,
    val merchantLogoUrl: String? = null,
    @SerializedName("balance_after") val balanceAfter: Long = 0,
    @SerializedName("status") val status: String = "completed",
    @SerializedName("created_at") val createdAt: String = "",
    @SerializedName("updated_at") val updatedAt: String = ""
) {
    fun amountMoney() = Money(amount = amount, currency = currency)
    fun balanceAfterMoney() = Money(amount = balanceAfter, currency = currency)
    val isCredit: Boolean get() = type == "CREDIT" || direction == "CREDIT"
    val isDebit: Boolean get() = !isCredit
}
