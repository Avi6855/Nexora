package com.nexora.app.core.design.animation

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.graphicsLayer

object AnimationUtils {
    @Composable
    fun pulseAnimation(): Float {
        val infiniteTransition = rememberInfiniteTransition(label = "pulse")
        val alpha by infiniteTransition.animateFloat(
            initialValue = 0.4f,
            targetValue = 1f,
            animationSpec = infiniteRepeatable(
                animation = tween(800, easing = LinearEasing),
                repeatMode = RepeatMode.Reverse
            ),
            label = "pulseAlpha"
        )
        return alpha
    }

    @Composable
    fun scaleAnimation(): Float {
        val infiniteTransition = rememberInfiniteTransition(label = "scale")
        val scale by infiniteTransition.animateFloat(
            initialValue = 0.95f,
            targetValue = 1.05f,
            animationSpec = infiniteRepeatable(
                animation = tween(600, easing = LinearEasing),
                repeatMode = RepeatMode.Reverse
            ),
            label = "scaleValue"
        )
        return scale
    }

    @Composable
    fun rotationAnimation(): Float {
        val infiniteTransition = rememberInfiniteTransition(label = "rotation")
        val rotation by infiniteTransition.animateFloat(
            initialValue = 0f,
            targetValue = 360f,
            animationSpec = infiniteRepeatable(
                animation = tween(1000, easing = LinearEasing),
                repeatMode = RepeatMode.Restart
            ),
            label = "rotationValue"
        )
        return rotation
    }

    @Composable
    fun scaleInAnimation(
        visible: Boolean,
        content: @Composable () -> Unit
    ) {
        val scale by animateFloatAsState(
            targetValue = if (visible) 1f else 0.8f,
            animationSpec = tween(300, easing = LinearEasing),
            label = "scaleIn"
        )
        val alpha by animateFloatAsState(
            targetValue = if (visible) 1f else 0f,
            animationSpec = tween(300),
            label = "alphaIn"
        )
        androidx.compose.animation.AnimatedVisibility(
            visible = visible,
            enter = expandVertically() + fadeIn(),
            exit = shrinkVertically() + fadeOut()
        ) {
            content()
        }
    }

    @Composable
    fun pulseModifier(): Modifier {
        val alpha = pulseAnimation()
        return Modifier.graphicsLayer { this.alpha = alpha }
    }

    @Composable
    fun scaleModifier(): Modifier {
        val scale = scaleAnimation()
        return Modifier.graphicsLayer {
            scaleX = scale
            scaleY = scale
        }
    }

    @Composable
    fun successScaleAnimation(): Float {
        val infiniteTransition = rememberInfiniteTransition(label = "successScale")
        val scale by infiniteTransition.animateFloat(
            initialValue = 0f,
            targetValue = 1f,
            animationSpec = infiniteRepeatable(
                animation = tween(600, easing = LinearEasing),
                repeatMode = RepeatMode.Restart
            ),
            label = "successScaleValue"
        )
        return scale
    }
}
