package com.nexora.app.core.design.shimmer

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
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
fun ShimmerBalance(
    modifier: Modifier = Modifier,
    useDark: Boolean = false
) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .height(180.dp)
            .clip(RoundedCornerShape(20.dp))
            .background(
                if (useDark) Color(0xFF1E293B) else Color(0xFFF1F5F9)
            )
    ) {
        Column(
            modifier = Modifier.padding(24.dp)
        ) {
            Spacer(
                modifier = Modifier
                    .height(14.dp)
                    .width(80.dp)
                    .clip(RoundedCornerShape(4.dp))
                    .shimmerEffect(useDark = useDark)
            )
            Spacer(modifier = Modifier.height(12.dp))
            Spacer(
                modifier = Modifier
                    .height(36.dp)
                    .width(140.dp)
                    .clip(RoundedCornerShape(8.dp))
                    .shimmerEffect(useDark = useDark)
            )
            Spacer(modifier = Modifier.height(24.dp))
            Row(
                modifier = Modifier.fillMaxWidth()
            ) {
                Column(modifier = Modifier.weight(1f)) {
                    Spacer(
                        modifier = Modifier
                            .height(10.dp)
                            .width(60.dp)
                            .clip(RoundedCornerShape(4.dp))
                            .shimmerEffect(useDark = useDark)
                    )
                    Spacer(modifier = Modifier.height(6.dp))
                    Spacer(
                        modifier = Modifier
                            .height(16.dp)
                            .width(70.dp)
                            .clip(RoundedCornerShape(4.dp))
                            .shimmerEffect(useDark = useDark)
                    )
                }
                Column(modifier = Modifier.weight(1f)) {
                    Spacer(
                        modifier = Modifier
                            .height(10.dp)
                            .width(50.dp)
                            .clip(RoundedCornerShape(4.dp))
                            .shimmerEffect(useDark = useDark)
                    )
                    Spacer(modifier = Modifier.height(6.dp))
                    Spacer(
                        modifier = Modifier
                            .height(16.dp)
                            .width(60.dp)
                            .clip(RoundedCornerShape(4.dp))
                            .shimmerEffect(useDark = useDark)
                    )
                }
            }
        }
    }
}
