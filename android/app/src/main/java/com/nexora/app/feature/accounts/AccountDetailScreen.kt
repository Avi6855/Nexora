package com.nexora.app.feature.accounts

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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import com.nexora.app.core.design.animation.formatCurrency
import com.nexora.app.core.design.component.NexoraBadge
import com.nexora.app.core.design.component.NexoraButton
import com.nexora.app.core.design.component.NexoraButtonStyle
import com.nexora.app.core.design.component.NexoraCard
import com.nexora.app.core.design.component.NexoraTopBar
import com.nexora.app.core.design.theme.NexoraPrimary

@Composable
fun AccountDetailScreen(
    accountId: String,
    onNavigateBack: () -> Unit,
    onNavigateToTransactions: (String) -> Unit,
    onNavigateToSendMoney: (String) -> Unit,
    viewModel: AccountDetailViewModel = hiltViewModel()
) {
    val uiState by viewModel.uiState.collectAsState()

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
    ) {
        NexoraTopBar(
            title = "Account Details",
            onBackClick = onNavigateBack
        )

        when (val state = uiState) {
            is AccountDetailUiState.Loading -> {
                Box(
                    modifier = Modifier.fillMaxSize(),
                    contentAlignment = Alignment.Center
                ) {
                    CircularProgressIndicator(color = NexoraPrimary)
                }
            }
            is AccountDetailUiState.Success -> {
                val account = state.account

                Column(
                    modifier = Modifier
                        .fillMaxSize()
                        .verticalScroll(rememberScrollState())
                        .padding(16.dp),
                    verticalArrangement = Arrangement.spacedBy(16.dp)
                ) {
                    NexoraCard(cornerRadius = 20.dp) {
                        Column(
                            modifier = Modifier.semantics {
                                contentDescription = "${account.name} account balance ${account.balance.formatCurrency(account.currency)}"
                            }
                        ) {
                            Row(
                                modifier = Modifier.fillMaxWidth(),
                                horizontalArrangement = Arrangement.SpaceBetween,
                                verticalAlignment = Alignment.CenterVertically
                            ) {
                                Text(
                                    text = account.name,
                                    style = MaterialTheme.typography.headlineMedium,
                                    fontWeight = FontWeight.Bold
                                )
                                NexoraBadge(
                                    text = account.status.replaceFirstChar { it.uppercase() },
                                    style = if (account.status == "active") com.nexora.app.core.design.component.BadgeStyle.Success
                                    else com.nexora.app.core.design.component.BadgeStyle.Warning
                                )
                            }
                            Spacer(modifier = Modifier.height(8.dp))
                            Text(
                                text = "Balance",
                                style = MaterialTheme.typography.bodyMedium,
                                color = MaterialTheme.colorScheme.onSurfaceVariant
                            )
                            Text(
                                text = account.balance.formatCurrency(account.currency),
                                style = MaterialTheme.typography.displaySmall,
                                fontWeight = FontWeight.Bold,
                                color = NexoraPrimary
                            )
                        }
                    }

                    NexoraCard {
                        Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                            BalanceRow(
                                label = "Available",
                                value = account.availableBalance.formatCurrency(account.currency)
                            )
                            BalanceRow(
                                label = "Pending",
                                value = account.pendingBalance.formatCurrency(account.currency)
                            )
                            BalanceRow(
                                label = "Reserved",
                                value = account.reservedBalance.formatCurrency(account.currency)
                            )
                        }
                    }

                    NexoraCard {
                        Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                            DetailRow(label = "Account Number", value = account.accountNumber)
                            DetailRow(label = "Sort Code", value = account.sortCode)
                            DetailRow(label = "Type", value = account.type.replaceFirstChar { it.uppercase() })
                            DetailRow(label = "Currency", value = account.currency)
                        }
                    }

                    NexoraButton(
                        onClick = { onNavigateToTransactions(accountId) },
                        text = "View Transactions",
                        style = NexoraButtonStyle.Secondary
                    )

                    NexoraButton(
                        onClick = { onNavigateToSendMoney(accountId) },
                        text = "Send Money"
                    )
                }
            }
            is AccountDetailUiState.Error -> {
                Box(
                    modifier = Modifier.fillMaxSize(),
                    contentAlignment = Alignment.Center
                ) {
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        Text(
                            text = state.message,
                            style = MaterialTheme.typography.bodyLarge,
                            color = MaterialTheme.colorScheme.error
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun BalanceRow(label: String, value: String) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceBetween
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant
        )
        Text(
            text = value,
            style = MaterialTheme.typography.bodyMedium,
            fontWeight = FontWeight.SemiBold
        )
    }
}

@Composable
private fun DetailRow(label: String, value: String) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceBetween
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant
        )
        Text(
            text = value,
            style = MaterialTheme.typography.bodyMedium,
            fontWeight = FontWeight.Medium
        )
    }
}
