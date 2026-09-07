package com.nexora.app.core.design.component

/**
 * Monzo-style category icons: every backend category maps to an emoji that
 * renders in the transaction list and detail screens.
 */
object CategoryIcons {

    /** Emoji for a backend category enum value ("EATING_OUT") or free text. */
    fun emojiFor(category: String): String = when (category.uppercase().replace(' ', '_')) {
        "GROCERIES" -> "🛒"
        "EATING_OUT" -> "🍽️"
        "TRANSPORT" -> "🚌"
        "SHOPPING" -> "🛍️"
        "BILLS" -> "🏠"
        "ENTERTAINMENT" -> "🎬"
        "TRAVEL" -> "✈️"
        "HEALTH" -> "💊"
        "SAVINGS" -> "💰"
        "TRANSFERS" -> "🔁"
        "INCOME" -> "💵"
        "OTHER" -> "🧾"
        else -> "🧾"
    }

    /** Human-readable name for a backend category enum value. */
    fun labelFor(category: String): String {
        if (category.isBlank()) return "Transaction"
        return category.lowercase().replace('_', ' ').replaceFirstChar { it.uppercase() }
    }

    /** All categories the filter chips offer (matches ledger-service domain). */
    val filterOptions: List<Pair<String, String>> = listOf(
        "GROCERIES" to "🛒 Groceries",
        "EATING_OUT" to "🍽️ Eating out",
        "TRANSPORT" to "🚌 Transport",
        "SHOPPING" to "🛍️ Shopping",
        "BILLS" to "🏠 Bills",
        "ENTERTAINMENT" to "🎬 Entertainment",
        "TRAVEL" to "✈️ Travel",
        "HEALTH" to "💊 Health",
        "SAVINGS" to "💰 Savings",
        "TRANSFERS" to "🔁 Transfers",
        "INCOME" to "💵 Income",
        "OTHER" to "🧾 Other"
    )
}
