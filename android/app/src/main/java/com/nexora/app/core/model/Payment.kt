package com.nexora.app.core.model

import com.google.gson.annotations.SerializedName
import kotlinx.serialization.Serializable

enum class PaymentState {
    @SerializedName("CREATED") CREATED,
    @SerializedName("AUTHORIZED") AUTHORIZED,
    @SerializedName("PROCESSING") PROCESSING,
    @SerializedName("UNKNOWN") UNKNOWN,
    @SerializedName("CONFIRMED") CONFIRMED,
    @SerializedName("SETTLED") SETTLED,
    @SerializedName("FAILED") FAILED,
    @SerializedName("REVERSED") REVERSED,
    @SerializedName("CANCELLED") CANCELLED
}

@Serializable
data class Payment(
    @SerializedName("payment_id") val id: String = "",
    @SerializedName("account_id") val accountId: String = "",
    @SerializedName("payment_type") val type: String = "transfer",
    @SerializedName("state") val state: PaymentState = PaymentState.CREATED,
    @SerializedName("amount") val amount: Long = 0,
    @SerializedName("currency") val currency: String = "GBP",
    @SerializedName("recipient_name") val recipientName: String = "",
    @SerializedName("recipient_account_number") val recipientAccountNumber: String = "",
    @SerializedName("recipient_sort_code") val recipientSortCode: String = "",
    @SerializedName("reference") val reference: String = "",
    @SerializedName("description") val description: String = "",
    @SerializedName("fee") val fee: Long = 0,
    @SerializedName("created_at") val createdAt: String = "",
    @SerializedName("updated_at") val updatedAt: String = ""
) {
    fun amountMoney() = Money(amount = amount, currency = currency)
    fun feeMoney() = Money(amount = fee, currency = currency)
}
