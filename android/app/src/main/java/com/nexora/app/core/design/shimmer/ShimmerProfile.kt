package com.nexora.app.core.design.shimmer

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp

@Composable
fun ShimmerProfile(
    modifier: Modifier = Modifier,
    useDark: Boolean = false
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(16.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        Spacer(
            modifier = Modifier
                .size(80.dp)
                .clip(CircleShape)
                .shimmerEffect(useDark = useDark)
        )
        Spacer(
            modifier = Modifier
                .height(20.dp)
                .width(120.dp)
                .clip(RoundedCornerShape(4.dp))
                .shimmerEffect(useDark = useDark)
        )
        Spacer(
            modifier = Modifier
                .height(14.dp)
                .width(160.dp)
                .clip(RoundedCornerShape(4.dp))
                .shimmerEffect(useDark = useDark)
        )

        Spacer(modifier = Modifier.height(24.dp))

        repeat(3) {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(12.dp))
                    .background(
                        if (useDark) Color(0xFF1E293B) else Color(0xFFF1F5F9)
                    )
                    .padding(16.dp),
                horizontalArrangement = Arrangement.spacedBy(16.dp),
                verticalAlignment = Alignment.CenterVertically
            ) {
                Spacer(
                    modifier = Modifier
                        .size(24.dp)
                        .clip(RoundedCornerShape(4.dp))
                        .shimmerEffect(useDark = useDark)
                )
                Spacer(
                    modifier = Modifier
                        .height(14.dp)
                        .width(120.dp)
                        .clip(RoundedCornerShape(4.dp))
                        .shimmerEffect(useDark = useDark)
                )
            }
        }
    }
}
