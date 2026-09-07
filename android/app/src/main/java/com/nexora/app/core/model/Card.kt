package com.nexora.app.core.model

import com.google.gson.annotations.SerializedName

data class Card(
    @SerializedName("card_id") val id: String = "",
    @SerializedName("user_id") val userId: String = "",
    @SerializedName("account_id") val accountId: String = "",
    @SerializedName("card_type") val type: String = "debit",
    @SerializedName("card_number_last4") val lastFourDigits: String = "",
    val expiryMonth: Int = 0,
    val expiryYear: Int = 0,
    @SerializedName("status") val status: String = "active",
    @SerializedName("spending_limit") val spendLimit: Long = 0,
    @SerializedName("daily_limit") val dailyLimit: Long = 0,
    @SerializedName("monthly_limit") val monthlyLimit: Long = 0,
    @SerializedName("currency") val currency: String = "GBP",
    @SerializedName("spending_controls") val controls: SpendingControls? = null,
    @SerializedName("created_at") val createdAt: String = "",
    @SerializedName("updated_at") val updatedAt: String = ""
) {
    val isFrozen: Boolean get() = status == "FROZEN" || status == "frozen"
    val formattedExpiry: String get() = String.format("%02d/%02d", expiryMonth, expiryYear % 100)

    val onlineEnabled: Boolean get() = controls?.onlineEnabled ?: true
    val atmEnabled: Boolean get() = controls?.atmEnabled ?: true
    val gamblingBlockEnabled: Boolean get() = controls?.gamblingBlockEnabled ?: false
}

/**
 * Channel spending controls, mirroring card-service domain.SpendingControls.
 * Booleans are nullable: null means "not set" (backend default = enabled).
 */
data class SpendingControls(
    @SerializedName("daily_limit") val dailyLimit: Long = 0,
    @SerializedName("monthly_limit") val monthlyLimit: Long = 0,
    @SerializedName("online_enabled") val onlineEnabled: Boolean? = null,
    @SerializedName("atm_enabled") val atmEnabled: Boolean? = null,
    @SerializedName("gambling_block_enabled") val gamblingBlockEnabled: Boolean? = null
)
