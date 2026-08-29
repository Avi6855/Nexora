package com.nexora.app.core.model

import kotlinx.serialization.Serializable

@Serializable
data class Pot(
    val id: String = "",
    val name: String = "",
    val goal: Long = 0,
    val balance: Long = 0,
    val currency: String = "GBP",
    val icon: String = "savings",
    val color: String = "#00BFA5",
    val isRoundUp: Boolean = false,
    val createdAt: String = "",
    val updatedAt: String = ""
) {
    fun balanceMoney() = Money(amount = balance, currency = currency)
    fun goalMoney() = Money(amount = goal, currency = currency)
    val progress: Float get() = if (goal > 0) (balance.toFloat() / goal.toFloat()).coerceIn(0f, 1f) else 0f
    val isGoalReached: Boolean get() = balance >= goal && goal > 0
}
