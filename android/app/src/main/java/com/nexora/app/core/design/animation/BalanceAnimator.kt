package com.nexora.app.core.design.animation

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.tween
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import kotlinx.coroutines.delay

@Composable
fun BalanceAnimator(
    targetValue: Long,
    durationMillis: Int = 600,
    onComplete: (Long) -> Unit = {}
): Animatable<Float, *> {
    val animatable = remember { Animatable(0f) }

    LaunchedEffect(targetValue) {
        animatable.animateTo(
            targetValue = targetValue.toFloat(),
            animationSpec = tween(
                durationMillis = durationMillis,
                easing = LinearEasing
            )
        )
        onComplete(targetValue)
    }

    return animatable
}

fun Long.formatCurrency(currency: String = "GBP"): String {
    val pounds = this / 100
    val pence = this % 100
    return when (currency) {
        "GBP" -> "\u00A3${pounds}.${String.format("%02d", pence)}"
        "USD" -> "$${pounds}.${String.format("%02d", pence)}"
        "EUR" -> "\u20AC${pounds}.${String.format("%02d", pence)}"
        else -> "$currency ${pounds}.${String.format("%02d", pence)}"
    }
}

fun Long.formatCurrencyCompact(currency: String = "GBP"): String {
    val pounds = this / 100
    return when {
        pounds >= 1_000_000 -> {
            val millions = pounds / 1_000_000.0
            String.format("%.1fM", millions)
        }
        pounds >= 1_000 -> {
            val thousands = pounds / 1_000.0
            String.format("%.1fK", thousands)
        }
        else -> formatCurrency(currency)
    }
}
