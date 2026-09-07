package com.nexora.app.feature.transactions

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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
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
import com.nexora.app.core.design.component.NexoraCard
import com.nexora.app.core.design.component.NexoraTopBar
import com.nexora.app.core.design.theme.NexoraPrimary
import com.nexora.app.core.design.theme.NexoraSuccess

@Composable
fun TransactionDetailScreen(
    transactionId: String,
    onNavigateBack: () -> Unit,
    onReportProblem: (String) -> Unit = {},
    viewModel: TransactionDetailViewModel = hiltViewModel()
) {
    val uiState by viewModel.uiState.collectAsState()

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
    ) {
        NexoraTopBar(
            title = "Transaction Details",
            onBackClick = onNavigateBack
        )

        when (val state = uiState) {
            is TransactionDetailUiState.Loading -> {
                Box(
                    modifier = Modifier.fillMaxSize(),
                    contentAlignment = Alignment.Center
                ) {
                    CircularProgressIndicator(color = NexoraPrimary)
                }
            }
            is TransactionDetailUiState.Success -> {
                val transaction = state.transaction
                val noteSaving by viewModel.noteSaving.collectAsState()
                val noteSaved by viewModel.noteSaved.collectAsState()

                Column(
                    modifier = Modifier
                        .fillMaxSize()
                        .verticalScroll(rememberScrollState())
                        .padding(16.dp),
                    verticalArrangement = Arrangement.spacedBy(16.dp)
                ) {
                    NexoraCard(cornerRadius = 20.dp) {
                        Column(
                            horizontalAlignment = Alignment.CenterHorizontally,
                            modifier = Modifier
                                .fillMaxWidth()
                                .semantics {
                                    contentDescription = "${if (transaction.isCredit) "Received" else "Sent"} ${transaction.amountMoney().formatted()}"
                                }
                        ) {
                            Box(
                                modifier = Modifier
                                    .size(64.dp)
                                    .clip(CircleShape)
                                    .background(
                                        if (transaction.isCredit) NexoraSuccess.copy(alpha = 0.12f)
                                        else MaterialTheme.colorScheme.error.copy(alpha = 0.12f)
                                    ),
                                contentAlignment = Alignment.Center
                            ) {
                                Text(
                                    text = if (transaction.isCredit) "+" else "-",
                                    style = MaterialTheme.typography.headlineLarge,
                                    fontWeight = FontWeight.Bold,
                                    color = if (transaction.isCredit) NexoraSuccess else MaterialTheme.colorScheme.error
                                )
                            }
                            Spacer(modifier = Modifier.height(16.dp))
                            Text(
                                text = transaction.amountMoney().formatted(),
                                style = MaterialTheme.typography.displaySmall,
                                fontWeight = FontWeight.Bold,
                                color = if (transaction.isCredit) NexoraSuccess else MaterialTheme.colorScheme.error
                            )
                            Spacer(modifier = Modifier.height(8.dp))
                            Text(
                                text = transaction.description.ifEmpty { transaction.merchantName ?: "Transaction" },
                                style = MaterialTheme.typography.bodyLarge,
                                fontWeight = FontWeight.Medium
                            )
                            Spacer(modifier = Modifier.height(8.dp))
                            NexoraBadge(
                                text = transaction.status.replaceFirstChar { it.uppercase() },
                                style = when (transaction.status) {
                                    "completed" -> com.nexora.app.core.design.component.BadgeStyle.Success
                                    "pending" -> com.nexora.app.core.design.component.BadgeStyle.Warning
                                    else -> com.nexora.app.core.design.component.BadgeStyle.Info
                                }
                            )
                        }
                    }

                    // ── Note editor ("Add a note") ──
                    NoteEditorCard(
                        initialNote = transaction.note,
                        isSaving = noteSaving,
                        justSaved = noteSaved,
                        onSave = { viewModel.saveNote(it) }
                    )

                    NexoraCard {
                        Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                            DetailRow(label = "Type", value = transaction.type.replaceFirstChar { it.uppercase() })
                            DetailRow(label = "Category", value = transaction.category.ifEmpty { "Transfer" })
                            DetailRow(label = "Date", value = transaction.createdAt)
                            DetailRow(
                                label = "Balance After",
                                value = transaction.balanceAfterMoney().formatted()
                            )
                            if (transaction.merchantName != null) {
                                DetailRow(label = "Merchant", value = transaction.merchantName)
                            }
                        }
                    }

                    // ── Dispute entry point ("report a problem") ──
                    if (transaction.isDebit) {
                        TextButton(onClick = { onReportProblem(transaction.id) }) {
                            Text(
                                "Something wrong? Report a problem with this payment",
                                color = NexoraPrimary
                            )
                        }
                    }
                }
            }
            is TransactionDetailUiState.Error -> {
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
private fun NoteEditorCard(
    initialNote: String,
    isSaving: Boolean,
    justSaved: Boolean,
    onSave: (String) -> Unit
) {
    var text by remember(initialNote) { mutableStateOf(initialNote) }

    NexoraCard {
        Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(
                text = "Note",
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.SemiBold
            )
            OutlinedTextField(
                value = text,
                onValueChange = { text = it.take(500) },
                modifier = Modifier.fillMaxWidth(),
                placeholder = { Text("Add a note to this transaction") },
                minLines = 2,
                singleLine = false
            )
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Text(
                    text = when {
                        isSaving -> "Saving…"
                        justSaved -> "Saved ✓"
                        else -> ""
                    },
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
                TextButton(
                    onClick = { onSave(text) },
                    enabled = !isSaving && text != initialNote
                ) {
                    Text("Save note")
                }
            }
        }
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
