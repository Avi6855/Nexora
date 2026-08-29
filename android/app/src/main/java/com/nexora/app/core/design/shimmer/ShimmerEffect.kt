package com.nexora.app.core.design.shimmer

import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.unit.IntSize

fun Modifier.shimmerEffect(
    shimmerColor: Color = Color(0xFFE2E8F0),
    shimmerDarkColor: Color = Color(0xFF334155),
    shimmerWidth: Float = 300f,
    useDark: Boolean = false
): Modifier = composed {
    val baseColor = if (useDark) shimmerDarkColor else shimmerColor
    var size by remember { mutableStateOf(IntSize.Zero) }
    val transition = rememberInfiniteTransition(label = "shimmer")
    val startOffsetX by transition.animateFloat(
        initialValue = -shimmerWidth,
        targetValue = size.width.toFloat() + shimmerWidth,
        animationSpec = infiniteRepeatable(
            animation = tween(
                durationMillis = 1200,
                easing = FastOutSlowInEasing
            ),
            repeatMode = RepeatMode.Reverse
        ),
        label = "shimmerOffset"
    )

    background(
        brush = Brush.linearGradient(
            colors = listOf(
                baseColor.copy(alpha = 0.3f),
                baseColor.copy(alpha = 0.7f),
                baseColor.copy(alpha = 0.3f)
            ),
            start = Offset(startOffsetX, 0f),
            end = Offset(startOffsetX + shimmerWidth, size.height.toFloat())
        )
    ).onGloballyPositioned { size = it.size }
}

fun Modifier.shimmerItem(
    shimmerColor: Color = Color(0xFFE2E8F0),
    useDark: Boolean = false
): Modifier = composed {
    this.shimmerEffect(shimmerColor = shimmerColor, useDark = useDark)
}
