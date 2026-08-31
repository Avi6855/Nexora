package com.nexora.app.core.model

import com.google.gson.annotations.SerializedName

data class Pot(
    @SerializedName("pot_id") val id: String = "",
    @SerializedName("name") val name: String = "",
    @SerializedName("target_amount") val goal: Long = 0,
    @SerializedName("current_amount") val balance: Long = 0,
    @SerializedName("currency") val currency: String = "GBP",
    val icon: String = "savings",
    val color: String = "#00BFA5",
    @SerializedName("round_up_enabled") val isRoundUp: Boolean = false,
    @SerializedName("created_at") val createdAt: String = "",
    @SerializedName("updated_at") val updatedAt: String = ""
) {
    fun balanceMoney() = Money(amount = balance, currency = currency)
    fun goalMoney() = Money(amount = goal, currency = currency)
    val progress: Float get() = if (goal > 0) (balance.toFloat() / goal.toFloat()).coerceIn(0f, 1f) else 0f
    val isGoalReached: Boolean get() = balance >= goal && goal > 0
}
