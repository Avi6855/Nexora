package com.nexora.app.feature.home

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.slideInVertically
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Notifications
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
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
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import com.nexora.app.core.design.animation.AnimationUtils
import com.nexora.app.core.design.animation.BalanceAnimator
import com.nexora.app.core.design.animation.formatCurrency
import com.nexora.app.core.design.component.NexoraBadge
import com.nexora.app.core.design.component.NexoraCard
import com.nexora.app.core.design.shimmer.ShimmerDashboard
import com.nexora.app.core.design.theme.NexoraGradientEnd
import com.nexora.app.core.design.theme.NexoraGradientMiddle
import com.nexora.app.core.design.theme.NexoraGradientStart
import com.nexora.app.core.design.theme.NexoraPrimary
import com.nexora.app.core.model.Transaction

@Composable
fun HomeScreen(
    onNavigateToAccount: (String) -> Unit,
    onNavigateToTransaction: (String) -> Unit,
    onNavigateToNotifications: () -> Unit,
    onNavigateToInsights: () -> Unit = {},
    viewModel: HomeViewModel = hiltViewModel()
) {
    val uiState by viewModel.uiState.collectAsState()
    val liveEvent by viewModel.liveEvent.collectAsState()
    var isRefreshing by remember { mutableStateOf(false) }
    var dataLoaded by remember { mutableStateOf(false) }

    LaunchedEffect(uiState) {
        if (uiState is HomeUiState.Success) {
            isRefreshing = false
            dataLoaded = true
        }
    }

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
    ) {
        when (val state = uiState) {
            is HomeUiState.Loading -> {
                ShimmerDashboard()
            }
            is HomeUiState.Success -> {
                LazyColumn(
                    modifier = Modifier
                        .fillMaxSize()
                        .padding(horizontal = 16.dp),
                    verticalArrangement = Arrangement.spacedBy(12.dp)
                ) {
                    item { Spacer(modifier = Modifier.height(8.dp)) }

                    item {
                        Row(
                            modifier = Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.SpaceBetween,
                            verticalAlignment = Alignment.CenterVertically
                        ) {
                            Column {
                                Text(
                                    text = getGreeting(),
                                    style = MaterialTheme.typography.bodyLarge,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant
                                )
                                Text(
                                    text = "Your finances",
                                    style = MaterialTheme.typography.headlineMedium,
                                    fontWeight = FontWeight.Bold,
                                    modifier = Modifier.semantics {
                                        contentDescription = "Your finances dashboard"
                                    }
                                )
                            }
                            IconButton(
                                onClick = onNavigateToNotifications,
                                modifier = Modifier.semantics {
                                    contentDescription = "Notifications"
                                }
                            ) {
                                Icon(
                                    imageVector = Icons.Filled.Notifications,
                                    contentDescription = "Notifications",
                                    tint = MaterialTheme.colorScheme.onSurface
                                )
                            }
                        }
                    }

                    // Live "money just moved" banner, driven by the SSE stream.
                    if (liveEvent != null) {
                        item {
                            LiveEventBanner(
                                event = liveEvent!!,  // checked above
                                onDismiss = { viewModel.consumeLiveEvent() }
                            )
                        }
                    }

                    item {
                        AnimatedVisibility(
                            visible = dataLoaded,
                            enter = fadeIn() + slideInVertically()
                        ) {
                            Column {
                                BalanceCard(
                                    totalBalance = state.totalBalance,
                                    availableBalance = state.availableBalance,
                                    pendingBalance = state.pendingBalance,
                                    reservedBalance = state.reservedBalance
                                )
                                Spacer(modifier = Modifier.height(8.dp))
                                SafeToSpendStrip(
                                    available = state.availableBalance,
                                    onOpenInsights = onNavigateToInsights
                                )
                                state.runway?.let { runway ->
                                    Spacer(modifier = Modifier.height(8.dp))
                                    RunwayStrip(
                                        runwayMonths = runway.runwayMonthsTotal,
                                        essentialsMonths = runway.runwayMonthsEssentials,
                                        verdict = runway.verdict
                                    )
                                }
                            }
                        }
                    }

                    if (state.accounts.isNotEmpty()) {
                        item {
                            Text(
                                text = "Accounts",
                                style = MaterialTheme.typography.titleMedium,
                                fontWeight = FontWeight.SemiBold,
                                modifier = Modifier.padding(top = 4.dp)
                            )
                        }

                        items(state.accounts) { account ->
                            AnimatedVisibility(
                                visible = dataLoaded,
                                enter = fadeIn() + slideInVertically()
                            ) {
                                AccountSummaryItem(
                                    name = account.name,
                                    balance = account.balance,
                                    currency = account.currency,
                                    onClick = { onNavigateToAccount(account.id) }
                                )
                            }
                        }
                    }

                    item {
                        Text(
                            text = "Recent Transactions",
                            style = MaterialTheme.typography.titleMedium,
                            fontWeight = FontWeight.SemiBold,
                            modifier = Modifier.padding(top = 4.dp)
                        )
                    }

                    if (state.recentTransactions.isEmpty()) {
                        item {
                            NexoraCard {
                                Text(
                                    text = "No recent transactions",
                                    style = MaterialTheme.typography.bodyMedium,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    modifier = Modifier.padding(8.dp)
                                )
                            }
                        }
                    } else {
                        items(state.recentTransactions) { transaction ->
                            AnimatedVisibility(
                                visible = dataLoaded,
                                enter = fadeIn() + slideInVertically()
                            ) {
                                TransactionItem(
                                    transaction = transaction,
                                    onClick = { onNavigateToTransaction(transaction.id) }
                                )
                            }
                        }
                    }

                    item { Spacer(modifier = Modifier.height(8.dp)) }
                }
            }
            is HomeUiState.Empty -> {
                Column(
                    modifier = Modifier
                        .fillMaxSize()
                        .padding(24.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.Center
                ) {
                    Text(
                        text = "Welcome to Nexora",
                        style = MaterialTheme.typography.headlineMedium,
                        fontWeight = FontWeight.Bold
                    )
                    Spacer(modifier = Modifier.height(8.dp))
                    Text(
                        text = "Create an account to get started",
                        style = MaterialTheme.typography.bodyLarge,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
            }
            is HomeUiState.Error -> {
                Column(
                    modifier = Modifier
                        .fillMaxSize()
                        .padding(24.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.Center
                ) {
                    Text(
                        text = "Oops!",
                        style = MaterialTheme.typography.headlineMedium,
                        fontWeight = FontWeight.Bold
                    )
                    Spacer(modifier = Modifier.height(8.dp))
                    Text(
                        text = state.message,
                        style = MaterialTheme.typography.bodyLarge,
                        color = MaterialTheme.colorScheme.error,
                        textAlign = TextAlign.Center
                    )
                    Spacer(modifier = Modifier.height(16.dp))
                    Text(
                        text = "Pull down to refresh",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
            }
        }
    }
}

/**
 * RunwayStrip is the "what if?" teaser: how long today's balance would cover
 * your spending if income stopped tomorrow (essentials-only vs everything).
 */
@Composable
private fun RunwayStrip(runwayMonths: Double, essentialsMonths: Double, verdict: String) {
    NexoraCard {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(12.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Text("🛟", style = MaterialTheme.typography.headlineSmall)
            Spacer(modifier = Modifier.width(12.dp))
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    "Financial runway",
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
                Text(
                    "%.1f months of coverage".format(essentialsMonths) +
                        " · %.1f all-in".format(runwayMonths),
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Bold
                )
                Text(
                    verdict,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
        }
    }
}

/**
 * SafeToSpendStrip teases the Financial Intelligence Platform from Home:
 * what's genuinely spendable vs reserved/pending, tapping through to the
 * full Insights screen.
 */
@Composable
private fun SafeToSpendStrip(available: Long, onOpenInsights: () -> Unit) {
    NexoraCard(
        modifier = Modifier.clickable { onOpenInsights() }
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(12.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Text("🧠", style = MaterialTheme.typography.headlineSmall)
            Spacer(modifier = Modifier.width(12.dp))
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    "Safe to spend",
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
                Text(
                    available.formatCurrency(),
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Bold
                )
            }
            Text(
                "See insights →",
                style = MaterialTheme.typography.labelMedium,
                color = NexoraPrimary,
                fontWeight = FontWeight.SemiBold
            )
        }
    }
}

@Composable
private fun LiveEventBanner(event: com.nexora.app.core.realtime.RealtimeEvent, onDismiss: () -> Unit) {
    NexoraCard(
        modifier = Modifier.clickable { onDismiss() }
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(12.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(12.dp)
        ) {
            Text(
                text = "⚡",
                style = MaterialTheme.typography.headlineSmall
            )
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    text = event.title.ifEmpty { "Live update" },
                    style = MaterialTheme.typography.titleSmall,
                    fontWeight = FontWeight.SemiBold
                )
                if (event.body.isNotBlank()) {
                    Text(
                        text = event.body,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
            }
            Text(
                text = "LIVE",
                style = MaterialTheme.typography.labelSmall,
                color = NexoraPrimary,
                fontWeight = FontWeight.Bold
            )
        }
    }
}

private fun getGreeting(): String {
    val hour = java.util.Calendar.getInstance().get(java.util.Calendar.HOUR_OF_DAY)
    return when {
        hour < 12 -> "Good morning"
        hour < 17 -> "Good afternoon"
        else -> "Good evening"
    }
}

@Composable
private fun BalanceCard(
    totalBalance: Long,
    availableBalance: Long,
    pendingBalance: Long,
    reservedBalance: Long
) {
    val animatable = BalanceAnimator(targetValue = totalBalance, durationMillis = 600)
    val balanceText = animatable.value.toLong().formatCurrency()

    Box(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(20.dp))
            .background(
                Brush.horizontalGradient(
                    colors = listOf(NexoraGradientStart, NexoraGradientMiddle, NexoraGradientEnd)
                )
            )
            .padding(24.dp)
            .semantics {
                contentDescription = "Total balance $balanceText"
            }
    ) {
        Column {
            Text(
                text = "Total Balance",
                style = MaterialTheme.typography.bodyMedium,
                color = Color.White.copy(alpha = 0.8f)
            )
            Spacer(modifier = Modifier.height(4.dp))
            Text(
                text = balanceText,
                style = MaterialTheme.typography.displayMedium,
                fontWeight = FontWeight.Bold,
                color = Color.White
            )
            Spacer(modifier = Modifier.height(20.dp))
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween
            ) {
                Column {
                    Text(
                        text = "Available",
                        style = MaterialTheme.typography.labelMedium,
                        color = Color.White.copy(alpha = 0.7f)
                    )
                    Text(
                        text = availableBalance.formatCurrency(),
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.SemiBold,
                        color = Color.White
                    )
                }
                Column {
                    Text(
                        text = "Pending",
                        style = MaterialTheme.typography.labelMedium,
                        color = Color.White.copy(alpha = 0.7f)
                    )
                    Text(
                        text = pendingBalance.formatCurrency(),
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.SemiBold,
                        color = Color.White
                    )
                }
                Column {
                    Text(
                        text = "Reserved",
                        style = MaterialTheme.typography.labelMedium,
                        color = Color.White.copy(alpha = 0.7f)
                    )
                    Text(
                        text = reservedBalance.formatCurrency(),
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.SemiBold,
                        color = Color.White
                    )
                }
            }
        }
    }
}

@Composable
private fun AccountSummaryItem(
    name: String,
    balance: Long,
    currency: String,
    onClick: () -> Unit
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(16.dp))
            .clickable(onClick = onClick)
            .padding(12.dp)
            .semantics {
                contentDescription = "$name account, balance ${balance.formatCurrency(currency)}"
            },
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        Box(
            modifier = Modifier
                .size(44.dp)
                .clip(CircleShape)
                .background(MaterialTheme.colorScheme.primaryContainer),
            contentAlignment = Alignment.Center
        ) {
            Text(
                text = name.firstOrNull()?.uppercase() ?: "A",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.primary
            )
        }
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = name,
                style = MaterialTheme.typography.bodyLarge,
                fontWeight = FontWeight.Medium
            )
            Text(
                text = "Current Account",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )
        }
        Text(
            text = balance.formatCurrency(currency),
            style = MaterialTheme.typography.bodyLarge,
            fontWeight = FontWeight.Bold
        )
    }
}

@Composable
private fun TransactionItem(
    transaction: Transaction,
    onClick: () -> Unit
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(12.dp))
            .clickable(onClick = onClick)
            .padding(12.dp)
            .semantics {
                contentDescription = "${transaction.description.ifEmpty { transaction.merchantName ?: "Transaction" }}, ${if (transaction.isCredit) "received" else "sent"} ${transaction.amountMoney().formatted()}"
            },
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        Box(
            modifier = Modifier
                .size(44.dp)
                .clip(CircleShape)
                .background(
                    if (transaction.isCredit) MaterialTheme.colorScheme.primaryContainer
                    else MaterialTheme.colorScheme.surfaceVariant
                ),
            contentAlignment = Alignment.Center
        ) {
            Text(
                text = com.nexora.app.core.design.component.CategoryIcons.emojiFor(transaction.category),
                style = MaterialTheme.typography.titleLarge
            )
        }
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = transaction.description.ifEmpty { transaction.merchantName ?: "Transaction" },
                style = MaterialTheme.typography.bodyLarge,
                fontWeight = FontWeight.Medium
            )
            Text(
                text = com.nexora.app.core.design.component.CategoryIcons.labelFor(transaction.category),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )
        }
        Column(horizontalAlignment = Alignment.End) {
            Text(
                text = transaction.amountMoney().formatted(),
                style = MaterialTheme.typography.bodyLarge,
                fontWeight = FontWeight.SemiBold,
                color = if (transaction.isCredit) NexoraPrimary else MaterialTheme.colorScheme.error
            )
            if (transaction.status != "completed") {
                NexoraBadge(
                    text = transaction.status.replaceFirstChar { it.uppercase() },
                    style = com.nexora.app.core.design.component.BadgeStyle.Warning
                )
            }
        }
    }
}
