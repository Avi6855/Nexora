package com.nexora.app.core.design.component

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.nexora.app.core.design.theme.NexoraError
import com.nexora.app.core.design.theme.NexoraSuccess
import com.nexora.app.core.design.theme.NexoraWarning

enum class BadgeStyle { Success, Error, Warning, Info }

@Composable
fun NexoraBadge(
    text: String,
    style: BadgeStyle = BadgeStyle.Info,
    modifier: Modifier = Modifier
) {
    val backgroundColor = when (style) {
        BadgeStyle.Success -> NexoraSuccess.copy(alpha = 0.12f)
        BadgeStyle.Error -> NexoraError.copy(alpha = 0.12f)
        BadgeStyle.Warning -> NexoraWarning.copy(alpha = 0.12f)
        BadgeStyle.Info -> MaterialTheme.colorScheme.primaryContainer
    }

    val textColor = when (style) {
        BadgeStyle.Success -> NexoraSuccess
        BadgeStyle.Error -> NexoraError
        BadgeStyle.Warning -> NexoraWarning
        BadgeStyle.Info -> MaterialTheme.colorScheme.primary
    }

    Text(
        text = text,
        modifier = modifier
            .clip(RoundedCornerShape(8.dp))
            .background(backgroundColor)
            .padding(horizontal = 10.dp, vertical = 4.dp),
        color = textColor,
        style = MaterialTheme.typography.labelSmall,
        fontWeight = FontWeight.SemiBold
    )
}
