package com.nexora.app.core.design.animation

import androidx.compose.animation.AnimatedContentTransitionScope
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.animation.slideOutVertically

object Transitions {
    private const val DURATION_SHORT = 200
    private const val DURATION_MEDIUM = 300
    private const val DURATION_LONG = 400

    val slideInFromRight: EnterTransition = slideInHorizontally(
        initialOffsetX = { fullWidth -> fullWidth },
        animationSpec = tween(DURATION_MEDIUM)
    ) + fadeIn(animationSpec = tween(DURATION_MEDIUM))

    val slideOutToRight: ExitTransition = slideOutHorizontally(
        targetOffsetX = { fullWidth -> fullWidth },
        animationSpec = tween(DURATION_MEDIUM)
    ) + fadeOut(animationSpec = tween(DURATION_MEDIUM))

    val slideInFromLeft: EnterTransition = slideInHorizontally(
        initialOffsetX = { fullWidth -> -fullWidth },
        animationSpec = tween(DURATION_MEDIUM)
    ) + fadeIn(animationSpec = tween(DURATION_MEDIUM))

    val slideOutToLeft: ExitTransition = slideOutHorizontally(
        targetOffsetX = { fullWidth -> -fullWidth },
        animationSpec = tween(DURATION_MEDIUM)
    ) + fadeOut(animationSpec = tween(DURATION_MEDIUM))

    val slideInFromBottom: EnterTransition = slideInVertically(
        initialOffsetY = { fullHeight -> fullHeight },
        animationSpec = tween(DURATION_MEDIUM)
    ) + fadeIn(animationSpec = tween(DURATION_MEDIUM))

    val slideOutToBottom: ExitTransition = slideOutVertically(
        targetOffsetY = { fullHeight -> fullHeight },
        animationSpec = tween(DURATION_MEDIUM)
    ) + fadeOut(animationSpec = tween(DURATION_MEDIUM))

    val fadeIn: EnterTransition = fadeIn(animationSpec = tween(DURATION_SHORT))
    val fadeOut: ExitTransition = fadeOut(animationSpec = tween(DURATION_SHORT))

    val fadeInMedium: EnterTransition = fadeIn(animationSpec = tween(DURATION_MEDIUM))
    val fadeOutMedium: ExitTransition = fadeOut(animationSpec = tween(DURATION_MEDIUM))

    val sharedAxisXEnter: EnterTransition = slideInHorizontally(
        initialOffsetX = { it },
        animationSpec = tween(DURATION_LONG)
    ) + fadeIn(animationSpec = tween(DURATION_LONG))

    val sharedAxisXExit: ExitTransition = slideOutHorizontally(
        targetOffsetX = { -it },
        animationSpec = tween(DURATION_LONG)
    ) + fadeOut(animationSpec = tween(DURATION_LONG))

    val sharedAxisXPopEnter: EnterTransition = slideInHorizontally(
        initialOffsetX = { -it },
        animationSpec = tween(DURATION_LONG)
    ) + fadeIn(animationSpec = tween(DURATION_LONG))

    val sharedAxisXPopExit: ExitTransition = slideOutHorizontally(
        targetOffsetX = { it },
        animationSpec = tween(DURATION_LONG)
    ) + fadeOut(animationSpec = tween(DURATION_LONG))
}
