package com.nexora.app.feature.payments

import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import com.nexora.app.core.design.theme.NexoraPrimary

@Composable
fun PaymentProcessingScreen(
    paymentId: String,
    onNavigateToSuccess: (String) -> Unit,
    onNavigateToFailed: (String) -> Unit,
    onNavigateToUnknown: (String) -> Unit,
    viewModel: PaymentProcessingViewModel = hiltViewModel()
) {
    val uiState by viewModel.uiState.collectAsState()

    LaunchedEffect(uiState) {
        when (val state = uiState) {
            is PaymentProcessingUiState.Success -> onNavigateToSuccess(paymentId)
            is PaymentProcessingUiState.Failed -> onNavigateToFailed(paymentId)
            is PaymentProcessingUiState.Unknown -> onNavigateToUnknown(paymentId)
            is PaymentProcessingUiState.Error -> { /* show error below */ }
            is PaymentProcessingUiState.Processing -> { /* keep showing spinner */ }
        }
    }

    val infiniteTransition = rememberInfiniteTransition(label = "processing")
    val rotation by infiniteTransition.animateFloat(
        initialValue = 0f,
        targetValue = 360f,
        animationSpec = infiniteRepeatable(
            animation = tween(1000, easing = LinearEasing),
            repeatMode = RepeatMode.Restart
        ),
        label = "rotation"
    )
    val pulse by infiniteTransition.animateFloat(
        initialValue = 0.8f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(
            animation = tween(800, easing = LinearEasing),
            repeatMode = RepeatMode.Reverse
        ),
        label = "pulse"
    )

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
            .padding(24.dp),
        contentAlignment = Alignment.Center
    ) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            modifier = Modifier.semantics {
                contentDescription = "Processing payment, please wait"
            }
        ) {
            CircularProgressIndicator(
                modifier = Modifier
                    .size(80.dp)
                    .graphicsLayer {
                        scaleX = pulse
                        scaleY = pulse
                    },
                color = NexoraPrimary,
                strokeWidth = 4.dp
            )
            Spacer(modifier = Modifier.height(32.dp))
            Text(
                text = "Processing Payment",
                style = MaterialTheme.typography.headlineMedium,
                fontWeight = FontWeight.Bold
            )
            Spacer(modifier = Modifier.height(8.dp))
            if (uiState is PaymentProcessingUiState.Error) {
                Text(
                    text = (uiState as PaymentProcessingUiState.Error).message,
                    style = MaterialTheme.typography.bodyLarge,
                    color = MaterialTheme.colorScheme.error,
                    textAlign = TextAlign.Center
                )
            } else {
                Text(
                    text = "Please wait while we process your payment.\nThis may take a few moments.",
                    style = MaterialTheme.typography.bodyLarge,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.Center
                )
            }
        }
    }
}
