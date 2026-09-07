package com.nexora.app.feature.disputes

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
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import com.nexora.app.core.design.animation.formatCurrency
import com.nexora.app.core.design.component.NexoraBadge
import com.nexora.app.core.design.component.NexoraCard
import com.nexora.app.core.design.component.NexoraTopBar
import com.nexora.app.core.design.component.BadgeStyle
import com.nexora.app.core.design.theme.NexoraPrimary
import com.nexora.app.core.model.DisputeCase

/**
 * DisputesScreen shows the chargeback case list (open + resolved) with the
 * current workflow stage of every case, plus the entry point for reporting a
 * new problem from a transaction.
 */
@Composable
fun DisputesScreen(
    onNavigateBack: () -> Unit,
    onNavigateToTransactions: () -> Unit,
    viewModel: DisputesViewModel = hiltViewModel()
) {
    val uiState by viewModel.uiState.collectAsState()

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
    ) {
        Column(modifier = Modifier.fillMaxSize()) {
            NexoraTopBar(title = "Disputes", onBackClick = onNavigateBack)

            when (val state = uiState) {
                is DisputesUiState.Loading -> StageInfo("Loading your cases…")
                is DisputesUiState.Empty -> Column {
                    StageInfo("No disputes. If a payment goes wrong, report it from the transaction.")
                    Text(
                        text = "Pick a transaction →",
                        style = MaterialTheme.typography.labelLarge,
                        color = NexoraPrimary,
                        fontWeight = FontWeight.SemiBold,
                        modifier = Modifier
                            .clickable { onNavigateToTransactions() }
                            .padding(horizontal = 24.dp)
                    )
                }
                is DisputesUiState.Error -> StageInfo(state.message)
                is DisputesUiState.Success -> LazyColumn(
                    modifier = Modifier.fillMaxSize(),
                    contentPadding = androidx.compose.foundation.layout.PaddingValues(
                        horizontal = 16.dp, vertical = 8.dp
                    ),
                    verticalArrangement = Arrangement.spacedBy(12.dp)
                ) {
                    if (state.openCases.isNotEmpty()) {
                        item {
                            Text(
                                "Open cases",
                                style = MaterialTheme.typography.titleMedium,
                                fontWeight = FontWeight.SemiBold
                            )
                        }
                        items(state.openCases) { case -> CaseCard(case) }
                    }
                    if (state.resolvedCases.isNotEmpty()) {
                        item {
                            Text(
                                "Resolved",
                                style = MaterialTheme.typography.titleMedium,
                                fontWeight = FontWeight.SemiBold,
                                modifier = Modifier.padding(top = 8.dp)
                            )
                        }
                        items(state.resolvedCases) { case -> CaseCard(case) }
                    }
                }
            }
        }

        ExtendedFloatingActionButton(
            onClick = onNavigateToTransactions,
            containerColor = NexoraPrimary,
            contentColor = MaterialTheme.colorScheme.onPrimary,
            modifier = Modifier
                .align(Alignment.BottomEnd)
                .padding(20.dp)
        ) {
            Icon(Icons.Filled.Add, contentDescription = null)
            Spacer(modifier = Modifier.width(6.dp))
            Text("Report a problem")
        }
    }
}

@Composable
private fun StageInfo(text: String) {
    Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Text(
            text = text,
            style = MaterialTheme.typography.bodyLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant
        )
    }
}

@Composable
private fun CaseCard(case: DisputeCase) {
    NexoraCard {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(12.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Box(
                modifier = Modifier
                    .size(44.dp)
                    .clip(CircleShape)
                    .background(
                        if (case.isOpen) MaterialTheme.colorScheme.error.copy(alpha = 0.12f)
                        else NexoraPrimary.copy(alpha = 0.12f)
                    ),
                contentAlignment = Alignment.Center
            ) {
                Text(
                    text = if (case.isOpen) "⏳" else "✅",
                    style = MaterialTheme.typography.titleLarge
                )
            }
            Spacer(modifier = Modifier.width(12.dp))
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    text = case.merchant.ifEmpty { "Transaction dispute" },
                    style = MaterialTheme.typography.bodyLarge,
                    fontWeight = FontWeight.SemiBold
                )
                Text(
                    text = stageLabel(case.stage) + if (case.resolution != null)
                        " · ${resolutionLabel(case.resolution)}" else "",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
            Column(horizontalAlignment = Alignment.End) {
                Text(
                    text = (-case.amount).formatCurrency(case.currency),
                    style = MaterialTheme.typography.bodyLarge,
                    fontWeight = FontWeight.Bold
                )
                if (case.provisionalCredit && case.isOpen) {
                    Spacer(modifier = Modifier.height(4.dp))
                    NexoraBadge(text = "Temp credit", style = BadgeStyle.Success)
                }
            }
        }
    }
}

fun stageLabel(stage: String): String = when (stage) {
    "SUBMITTED" -> "Submitted — checking eligibility"
    "ELIGIBILITY" -> "Eligibility check in progress"
    "AWAITING_EVIDENCE" -> "Waiting for your evidence"
    "MERCHANT_RESPONSE" -> "Waiting for the merchant"
    "UNDER_REVIEW" -> "Under review"
    "RESOLVED" -> "Resolved"
    else -> stage.lowercase().replace('_', ' ')
}

fun resolutionLabel(resolution: String): String = when (resolution) {
    "REFUNDED" -> "Refunded"
    "REJECTED" -> "Rejected"
    "MERCHANT_REFUND" -> "Merchant refunded"
    "WITHDRAWN" -> "Withdrawn"
    else -> resolution.lowercase().replace('_', ' ')
}
