package com.nexora.app.core.design.shimmer

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp

@Composable
fun ShimmerDashboard(
    modifier: Modifier = Modifier,
    useDark: Boolean = false
) {
    Column(
        modifier = modifier.padding(horizontal = 16.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp)
    ) {
        ShimmerBalance(useDark = useDark)

        Spacer(
            modifier = Modifier
                .height(18.dp)
                .width(120.dp)
                .clip(RoundedCornerShape(4.dp))
                .shimmerEffect(useDark = useDark)
        )

        repeat(3) {
            ShimmerAccount(useDark = useDark)
        }

        Spacer(
            modifier = Modifier
                .height(18.dp)
                .width(140.dp)
                .clip(RoundedCornerShape(4.dp))
                .shimmerEffect(useDark = useDark)
        )

        repeat(3) {
            ShimmerTransaction(useDark = useDark)
        }
    }
}
