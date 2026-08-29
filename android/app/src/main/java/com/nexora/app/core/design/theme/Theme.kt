package com.nexora.app.core.design.theme

import android.app.Activity
import android.os.Build
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.core.view.WindowCompat
import com.nexora.app.core.datastore.PreferencesDataStore
import javax.inject.Inject

private val NexoraLightColorScheme = lightColorScheme(
    primary = NexoraPrimary,
    onPrimary = NexoraOnPrimary,
    primaryContainer = NexoraPrimaryContainer,
    onPrimaryContainer = NexoraOnPrimaryContainer,
    secondary = NexoraSecondary,
    onSecondary = NexoraOnSecondary,
    secondaryContainer = NexoraSecondaryContainer,
    onSecondaryContainer = NexoraOnSecondaryContainer,
    tertiary = NexoraTertiary,
    onTertiary = NexoraOnTertiary,
    tertiaryContainer = NexoraTertiaryContainer,
    onTertiaryContainer = NexoraOnTertiaryContainer,
    background = NexoraBackgroundLight,
    onBackground = NexoraOnBackgroundLight,
    surface = NexoraSurfaceLight,
    onSurface = NexoraOnSurfaceLight,
    surfaceVariant = NexoraSurfaceVariantLight,
    onSurfaceVariant = NexoraOnSurfaceVariantLight,
    error = NexoraError,
    onError = NexoraOnError,
    errorContainer = NexoraErrorContainer,
    onErrorContainer = NexoraOnErrorContainer,
    outline = NexoraDividerLight,
    outlineVariant = NexoraCardBorderLight,
    surfaceTint = NexoraPrimary
)

private val NexoraDarkColorScheme = darkColorScheme(
    primary = NexoraPrimaryLight,
    onPrimary = NexoraPrimaryDark,
    primaryContainer = NexoraPrimaryDark,
    onPrimaryContainer = NexoraPrimaryLight,
    secondary = NexoraSecondary,
    onSecondary = NexoraOnSecondary,
    secondaryContainer = NexoraSecondaryDark,
    onSecondaryContainer = NexoraSecondaryContainer,
    tertiary = NexoraTertiary,
    onTertiary = NexoraOnTertiary,
    tertiaryContainer = NexoraTertiaryDark,
    onTertiaryContainer = NexoraTertiaryContainer,
    background = NexoraBackgroundDark,
    onBackground = NexoraOnBackgroundDark,
    surface = NexoraSurfaceDark,
    onSurface = NexoraOnSurfaceDark,
    surfaceVariant = NexoraSurfaceVariantDark,
    onSurfaceVariant = NexoraOnSurfaceVariantDark,
    error = NexoraError,
    onError = NexoraOnError,
    errorContainer = NexoraErrorContainer,
    onErrorContainer = NexoraOnErrorContainer,
    outline = NexoraDividerDark,
    outlineVariant = NexoraCardBorderDark,
    surfaceTint = NexoraPrimaryLight
)

@Composable
fun NexoraTheme(
    darkTheme: Boolean = isSystemInDarkTheme(),
    dynamicColor: Boolean = false,
    content: @Composable () -> Unit
) {
    val colorScheme = when {
        dynamicColor && Build.VERSION.SDK_INT >= Build.VERSION_CODES.S -> {
            val context = LocalContext.current
            if (darkTheme) dynamicDarkColorScheme(context) else dynamicLightColorScheme(context)
        }
        darkTheme -> NexoraDarkColorScheme
        else -> NexoraLightColorScheme
    }

    val view = LocalView.current
    if (!view.isInEditMode) {
        SideEffect {
            val window = (view.context as Activity).window
            WindowCompat.getInsetsController(window, view).apply {
                isAppearanceLightStatusBars = !darkTheme
                isAppearanceLightNavigationBars = !darkTheme
            }
        }
    }

    MaterialTheme(
        colorScheme = colorScheme,
        typography = NexoraTypography,
        shapes = NexoraShapes,
        content = content
    )
}
