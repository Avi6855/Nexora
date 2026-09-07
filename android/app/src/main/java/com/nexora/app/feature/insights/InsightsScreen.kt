package com.nexora.app.feature.insights

import androidx.compose.foundation.background
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
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
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
import com.nexora.app.core.design.animation.formatCurrency
import com.nexora.app.core.design.component.CategoryIcons
import com.nexora.app.core.design.component.NexoraCard
import com.nexora.app.core.design.component.NexoraTopBar
import com.nexora.app.core.design.theme.NexoraGradientEnd
import com.nexora.app.core.design.theme.NexoraGradientMiddle
import com.nexora.app.core.design.theme.NexoraGradientStart
import com.nexora.app.core.design.theme.NexoraPrimary
import com.nexora.app.core.model.SafeToSpend
import com.nexora.app.core.model.Subscription

/**
 * InsightsScreen — the Financial Intelligence Platform in the app:
 * Safe-to-Spend (Monzo Summary-style), detected subscriptions with price-hike
 * flags, and the alert feed.
 */
@Composable
fun InsightsScreen(
    onNavigateBack: () -> Unit,
    viewModel: InsightsViewModel = hiltViewModel()
) {
    val uiState by viewModel.uiState.collectAsState()

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
    ) {
        NexoraTopBar(
            title = "Insights",
            onBackClick = onNavigateBack,
            actions = {
                IconButton(onClick = { viewModel.loadData() }) {
                    Icon(
                        imageVector = Icons.Filled.Refresh,
                        contentDescription = "Refresh insights"
                    )
                }
            }
        )

        when (val state = uiState) {
            is InsightsUiState.Loading -> {
                Column(
                    modifier = Modifier
                        .fillMaxSize()
                        .padding(24.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.Center
                ) {
                    Text("Crunching your money…", style = MaterialTheme.typography.bodyLarge)
                }
            }
            is InsightsUiState.Empty -> {
                Column(
                    modifier = Modifier
                        .fillMaxSize()
                        .padding(24.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.Center
                ) {
                    Text("Nothing to analyse yet", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold)
                    Spacer(Modifier.height(8.dp))
                    Text(
                        "Spend a little with your card and insights will appear here.",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        textAlign = TextAlign.Center
                    )
                }
            }
            is InsightsUiState.Error -> {
                Column(
                    modifier = Modifier
                        .fillMaxSize()
                        .padding(24.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.Center
                ) {
                    Text("Couldn't load insights", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold)
                    Spacer(Modifier.height(8.dp))
                    Text(
                        state.message,
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.error,
                        textAlign = TextAlign.Center
                    )
                }
            }
            is InsightsUiState.Success -> {
                LazyColumn(
                    modifier = Modifier.fillMaxSize(),
                    contentPadding = androidx.compose.foundation.layout.PaddingValues(16.dp),
                    verticalArrangement = Arrangement.spacedBy(12.dp)
                ) {
                    items(state.safeToSpendByAccount) { item ->
                        SafeToSpendCard(item.account.name, item.safeToSpend)
                    }

                    item {
                        Text(
                            text = "Subscriptions",
                            style = MaterialTheme.typography.titleMedium,
                            fontWeight = FontWeight.SemiBold,
                            modifier = Modifier.padding(top = 4.dp)
                        )
                    }

                    if (state.subscriptions.isEmpty()) {
                        item {
                            NexoraCard {
                                Text(
                                    "No recurring payments detected yet. When we spot one (like Netflix every month) it will show up here.",
                                    style = MaterialTheme.typography.bodyMedium,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    modifier = Modifier.padding(8.dp)
                                )
                            }
                        }
                    } else {
                        item {
                            NexoraCard {
                                Row(
                                    modifier = Modifier
                                        .fillMaxWidth()
                                        .padding(12.dp),
                                    horizontalArrangement = Arrangement.SpaceBetween
                                ) {
                                    Column {
                                        Text("Total per month", style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                        Text(
                                            state.monthlySubscriptionCost.formatCurrency(),
                                            style = MaterialTheme.typography.titleLarge,
                                            fontWeight = FontWeight.Bold
                                        )
                                        Text(
                                            "about ${(state.monthlySubscriptionCost * 12).formatCurrency()} a year",
                                            style = MaterialTheme.typography.bodySmall,
                                            color = MaterialTheme.colorScheme.onSurfaceVariant
                                        )
                                    }
                                }
                            }
                        }
                        items(state.subscriptions) { subscription ->
                            SubscriptionItem(subscription)
                        }
                    }

                    if (state.alerts.isNotEmpty()) {
                        item {
                            Text(
                                text = "Recent intelligence",
                                style = MaterialTheme.typography.titleMedium,
                                fontWeight = FontWeight.SemiBold,
                                modifier = Modifier.padding(top = 4.dp)
                            )
                        }
                        items(state.alerts) { alert ->
                            AlertItem(alert)
                        }
                    }

                    item { Spacer(Modifier.height(8.dp)) }
                }
            }
        }
    }
}

@Composable
private fun SafeToSpendCard(accountName: String, sts: SafeToSpend) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(20.dp))
            .background(
                Brush.horizontalGradient(
                    colors = listOf(NexoraGradientStart, NexoraGradientMiddle, NexoraGradientEnd)
                )
            )
            .padding(20.dp)
            .semantics {
                contentDescription = "Safe to spend ${sts.safeToSpendDaily.formatCurrency()} today, " +
                    "${sts.safeToSpend.formatCurrency()} until payday"
            }
    ) {
        Column {
            Text("Safe to spend · $accountName", color = Color.White.copy(alpha = 0.8f), style = MaterialTheme.typography.bodyMedium)
            Text(
                sts.safeToSpendDaily.formatCurrency(),
                style = MaterialTheme.typography.displaySmall,
                fontWeight = FontWeight.Bold,
                color = Color.White
            )
            Text("today", color = Color.White.copy(alpha = 0.8f), style = MaterialTheme.typography.labelMedium)

            Spacer(Modifier.height(12.dp))
            Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                Column {
                    Text("Until payday", style = MaterialTheme.typography.labelMedium, color = Color.White.copy(alpha = 0.7f))
                    Text(sts.safeToSpend.formatCurrency(), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold, color = Color.White)
                }
                Column {
                    Text("Upcoming bills", style = MaterialTheme.typography.labelMedium, color = Color.White.copy(alpha = 0.7f))
                    Text(sts.upcomingBills.formatCurrency(), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold, color = Color.White)
                }
                Column {
                    Text(if (sts.daysToPayday > 0) "Days to payday" else "Payday", style = MaterialTheme.typography.labelMedium, color = Color.White.copy(alpha = 0.7f))
                    Text(
                        if (sts.daysToPayday > 0) "${sts.daysToPayday}" else "🎉",
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
private fun SubscriptionItem(subscription: Subscription) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(12.dp))
            .padding(12.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        Box(
            modifier = Modifier
                .size(44.dp)
                .clip(CircleShape)
                .background(MaterialTheme.colorScheme.surfaceVariant),
            contentAlignment = Alignment.Center
        ) {
            Text(
                text = CategoryIcons.emojiFor("ENTERTAINMENT"),
                style = MaterialTheme.typography.titleLarge
            )
        }
        Column(modifier = Modifier.weight(1f)) {
            Text(subscription.merchant, style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.Medium)
            Text(
                "about every ${subscription.cadenceDays} days · ${subscription.transactionCount} payments",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )
            if (subscription.hasPriceHike) {
                Text(
                    "↑ Price up ${subscription.priceHikePct.toInt()}% (was ${subscription.previousAmount.formatCurrency()})",
                    style = MaterialTheme.typography.bodySmall,
                    fontWeight = FontWeight.SemiBold,
                    color = MaterialTheme.colorScheme.error
                )
            }
        }
        Column(horizontalAlignment = Alignment.End) {
            Text(
                subscription.lastAmount.formatCurrency(),
                style = MaterialTheme.typography.bodyLarge,
                fontWeight = FontWeight.Bold
            )
            Text(
                "${subscription.monthlyAmount.formatCurrency()}/mo",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )
        }
    }
}

@Composable
private fun AlertItem(alert: com.nexora.app.core.model.InsightAlert) {
    NexoraCard {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(12.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(12.dp)
        ) {
            Text("💡", style = MaterialTheme.typography.headlineSmall)
            Column(modifier = Modifier.weight(1f)) {
                Text(alert.title, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
                Text(
                    alert.body,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
        }
    }
}
