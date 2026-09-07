package com.nexora.app.feature.cards

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import com.nexora.app.core.design.component.NexoraButton
import com.nexora.app.core.design.component.NexoraButtonStyle
import com.nexora.app.core.design.component.NexoraCard
import com.nexora.app.core.design.component.NexoraTopBar
import com.nexora.app.core.design.animation.formatCurrency
import com.nexora.app.core.design.theme.NexoraPrimary
import com.nexora.app.core.model.Card

/**
 * Card detail ("Manage card"): freeze/unfreeze, per-card daily & monthly
 * spending limits, and the Monzo-style channel controls (online, ATM,
 * gambling block) that are enforced live by card-service at authorization.
 */
@Composable
fun CardDetailScreen(
    cardId: String,
    onNavigateBack: () -> Unit,
    viewModel: CardsViewModel = hiltViewModel()
) {
    val state by viewModel.detailState.collectAsState()
    val actionError by viewModel.actionError.collectAsState()

    var showLimitDialog by remember { mutableStateOf(false) }

    LaunchedEffect(actionError) {
        // Errors are surfaced inline below; nothing async to do here.
    }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
    ) {
        NexoraTopBar(title = "Manage Card", onBackClick = onNavigateBack)

        when (val s = state) {
            is CardDetailUiState.Loading -> {
                Column(
                    modifier = Modifier
                        .fillMaxSize()
                        .padding(16.dp),
                    verticalArrangement = Arrangement.spacedBy(16.dp)
                ) {
                    com.nexora.app.core.design.shimmer.ShimmerCard()
                }
            }
            is CardDetailUiState.Success -> {
                val card = s.card
                Column(
                    modifier = Modifier
                        .fillMaxSize()
                        .verticalScroll(rememberScrollState())
                        .padding(16.dp),
                    verticalArrangement = Arrangement.spacedBy(16.dp)
                ) {
                    CardVisual(card)

                    // ── Freeze ──
                    if (card.isFrozen) {
                        NexoraButton(text = "Unfreeze Card", style = NexoraButtonStyle.Secondary, onClick = { viewModel.unfreezeCard(card.id) })
                    } else {
                        NexoraButton(text = "Freeze Card", style = NexoraButtonStyle.Secondary, onClick = { viewModel.freezeCard(card.id) })
                    }

                    // ── Spending limits ──
                    SectionCard(title = "Spending limits") {
                        LimitRow(label = "Daily limit", amount = card.dailyLimit)
                        LimitRow(label = "Monthly limit", amount = card.monthlyLimit)
                        Button(onClick = { showLimitDialog = true }) {
                            Text("Edit limits")
                        }
                    }

                    // ── Channel controls ──
                    SectionCard(title = "Where this card works") {
                        ControlToggle(
                            title = "Online payments",
                            subtitle = "Allow e-commerce and in-app purchases",
                            checked = card.onlineEnabled,
                            onCheckedChange = { viewModel.updateControls(online = it) }
                        )
                        HorizontalDivider()
                        ControlToggle(
                            title = "ATM withdrawals",
                            subtitle = "Allow cash withdrawals at machines",
                            checked = card.atmEnabled,
                            onCheckedChange = { viewModel.updateControls(atm = it) }
                        )
                        HorizontalDivider()
                        ControlToggle(
                            title = "Gambling block",
                            subtitle = "Always decline gambling merchants",
                            checked = card.gamblingBlockEnabled,
                            onCheckedChange = { viewModel.updateControls(gamblingBlock = it) }
                        )
                    }

                    actionError?.let { msg ->
                        Text(
                            text = msg,
                            color = MaterialTheme.colorScheme.error,
                            style = MaterialTheme.typography.bodyMedium
                        )
                    }
                }
            }
            is CardDetailUiState.Error -> {
                Column(
                    modifier = Modifier
                        .fillMaxSize()
                        .padding(24.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.Center
                ) {
                    Text(
                        text = s.message,
                        color = MaterialTheme.colorScheme.error
                    )
                }
            }
        }
    }

    if (showLimitDialog) {
        val current = (state as? CardDetailUiState.Success)?.card
        LimitEditDialog(
            initialDaily = current?.dailyLimit ?: 0L,
            initialMonthly = current?.monthlyLimit ?: 0L,
            onDismiss = { showLimitDialog = false },
            onSave = { daily, monthly ->
                viewModel.updateLimits(daily, monthly)
                showLimitDialog = false
            }
        )
    }
}

@Composable
private fun CardVisual(card: Card) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(20.dp))
            .background(
                Brush.horizontalGradient(
                    colors = listOf(
                        NexoraPrimary,
                        NexoraPrimary.copy(alpha = 0.8f),
                        MaterialTheme.colorScheme.secondary
                    )
                )
            )
            .padding(24.dp)
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically
        ) {
            Text(
                text = "NEXORA",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = Color.White
            )
            if (card.isFrozen) {
                Text(
                    text = "FROZEN",
                    color = Color.White,
                    style = MaterialTheme.typography.labelMedium,
                    fontWeight = FontWeight.Bold
                )
            }
        }
        Spacer(modifier = Modifier.height(48.dp))
        Text(
            text = "\u2022\u2022\u2022\u2022 \u2022\u2022\u2022\u2022 \u2022\u2022\u2022\u2022 ${card.lastFourDigits}",
            style = MaterialTheme.typography.titleLarge,
            color = Color.White
        )
    }
}

@Composable
private fun SectionCard(title: String, content: @Composable () -> Unit) {
    NexoraCard {
        Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text(title, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            content()
        }
    }
}

@Composable
private fun LimitRow(label: String, amount: Long) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceBetween
    ) {
        Text(label, style = MaterialTheme.typography.bodyMedium)
        Text(
            text = amount.formatCurrency(),
            style = MaterialTheme.typography.bodyMedium,
            fontWeight = FontWeight.SemiBold
        )
    }
}

@Composable
private fun ControlToggle(
    title: String,
    subtitle: String,
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit
) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.SpaceBetween
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge)
            Text(
                text = subtitle,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )
        }
        Switch(checked = checked, onCheckedChange = onCheckedChange)
    }
}

@Composable
private fun LimitEditDialog(
    initialDaily: Long,
    initialMonthly: Long,
    onDismiss: () -> Unit,
    onSave: (Long, Long) -> Unit
) {
    var daily by remember { mutableStateOf((initialDaily / 100).toString()) }
    var monthly by remember { mutableStateOf((initialMonthly / 100).toString()) }

    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Edit spending limits") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                OutlinedTextField(
                    value = daily,
                    onValueChange = { daily = it.filter { ch -> ch.isDigit() } },
                    label = { Text("Daily limit (£)") },
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                    singleLine = true
                )
                OutlinedTextField(
                    value = monthly,
                    onValueChange = { monthly = it.filter { ch -> ch.isDigit() } },
                    label = { Text("Monthly limit (£)") },
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                    singleLine = true
                )
            }
        },
        confirmButton = {
            TextButton(onClick = {
                val d = daily.toLongOrNull() ?: return@TextButton
                val m = monthly.toLongOrNull() ?: return@TextButton
                if (d > 0 && m > 0) onSave(d * 100, m * 100)
            }) { Text("Save") }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text("Cancel") }
        }
    )
}
