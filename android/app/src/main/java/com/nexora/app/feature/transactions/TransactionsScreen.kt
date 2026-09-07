package com.nexora.app.feature.transactions

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.slideInVertically
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
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
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.Icon
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Search
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import androidx.compose.foundation.text.KeyboardOptions
import androidx.hilt.navigation.compose.hiltViewModel
import com.nexora.app.core.design.component.CategoryIcons
import com.nexora.app.core.design.component.NexoraTopBar
import com.nexora.app.core.design.shimmer.ShimmerTransaction
import com.nexora.app.core.design.theme.NexoraPrimary
import com.nexora.app.core.model.Transaction

@Composable
fun TransactionsScreen(
    accountId: String,
    onNavigateBack: () -> Unit,
    onNavigateToTransaction: (String) -> Unit,
    viewModel: TransactionsViewModel = hiltViewModel()
) {
    val uiState by viewModel.uiState.collectAsState()
    val searchQuery by viewModel.searchQuery.collectAsState()
    val selectedCategory by viewModel.selectedCategory.collectAsState()
    val selectedType by viewModel.selectedType.collectAsState()

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
    ) {
        Column(
            modifier = Modifier.fillMaxSize()
        ) {
            NexoraTopBar(
                title = "Transactions",
                onBackClick = onNavigateBack
            )

            // ── Search ──
            OutlinedTextField(
                value = searchQuery,
                onValueChange = viewModel::onSearchChange,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 16.dp, vertical = 4.dp),
                placeholder = { Text("Search transactions") },
                leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null) },
                trailingIcon = {
                    if (searchQuery.isNotEmpty()) {
                        Icon(
                            Icons.Filled.Close,
                            contentDescription = "Clear search",
                            modifier = Modifier.clickable { viewModel.onSearchChange("") }
                        )
                    }
                },
                singleLine = true,
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
                shape = RoundedCornerShape(12.dp)
            )

            // ── Category filter chips ──
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .horizontalScroll(rememberScrollState())
                    .padding(horizontal = 16.dp, vertical = 4.dp),
                horizontalArrangement = Arrangement.spacedBy(8.dp)
            ) {
                FilterChip(
                    selected = selectedCategory == null,
                    onClick = { viewModel.onCategorySelected(null) },
                    label = { Text("All") }
                )
                CategoryIcons.filterOptions.forEach { (value, label) ->
                    FilterChip(
                        selected = selectedCategory == value,
                        onClick = {
                            viewModel.onCategorySelected(if (selectedCategory == value) null else value)
                        },
                        label = { Text(label) }
                    )
                }
            }

            // ── Money in / out toggle ──
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 16.dp, vertical = 4.dp),
                horizontalArrangement = Arrangement.spacedBy(8.dp)
            ) {
                FilterChip(
                    selected = selectedType == null,
                    onClick = { viewModel.onTypeSelected(null) },
                    label = { Text("Money in & out") }
                )
                FilterChip(
                    selected = selectedType == "CREDIT",
                    onClick = {
                        viewModel.onTypeSelected(if (selectedType == "CREDIT") null else "CREDIT")
                    },
                    label = { Text("Money in") }
                )
                FilterChip(
                    selected = selectedType == "DEBIT",
                    onClick = {
                        viewModel.onTypeSelected(if (selectedType == "DEBIT") null else "DEBIT")
                    },
                    label = { Text("Money out") }
                )
            }

            when (val state = uiState) {
                is TransactionsUiState.Loading -> {
                    LazyColumn(
                        modifier = Modifier.padding(16.dp),
                        verticalArrangement = Arrangement.spacedBy(8.dp)
                    ) {
                        items(8) { ShimmerTransaction() }
                    }
                }
                is TransactionsUiState.Success -> {
                    LazyColumn(
                        modifier = Modifier.padding(16.dp),
                        verticalArrangement = Arrangement.spacedBy(4.dp)
                    ) {
                        itemsIndexed(state.transactions) { index, transaction ->
                            AnimatedVisibility(
                                visible = true,
                                enter = fadeIn() + slideInVertically(
                                    initialOffsetY = { it / 2 }
                                )
                            ) {
                                TransactionItem(
                                    transaction = transaction,
                                    onClick = { onNavigateToTransaction(transaction.id) }
                                )
                            }
                        }
                    }
                }
                is TransactionsUiState.Empty -> {
                    Box(
                        modifier = Modifier.fillMaxSize(),
                        contentAlignment = Alignment.Center
                    ) {
                        Column(horizontalAlignment = Alignment.CenterHorizontally) {
                            Text(
                                text = if (searchQuery.isNotBlank() || selectedCategory != null || selectedType != null)
                                    "No matching transactions"
                                else
                                    "No transactions yet",
                                style = MaterialTheme.typography.headlineMedium,
                                fontWeight = FontWeight.Bold
                            )
                            Spacer(modifier = Modifier.height(8.dp))
                            Text(
                                text = "Try a different search or filter",
                                style = MaterialTheme.typography.bodyLarge,
                                color = MaterialTheme.colorScheme.onSurfaceVariant
                            )
                        }
                    }
                }
                is TransactionsUiState.Error -> {
                    Box(
                        modifier = Modifier.fillMaxSize(),
                        contentAlignment = Alignment.Center
                    ) {
                        Column(horizontalAlignment = Alignment.CenterHorizontally) {
                            Text(
                                text = "Error",
                                style = MaterialTheme.typography.headlineMedium,
                                color = MaterialTheme.colorScheme.error
                            )
                            Spacer(modifier = Modifier.height(8.dp))
                            Text(
                                text = state.message,
                                style = MaterialTheme.typography.bodyLarge,
                                color = MaterialTheme.colorScheme.onSurfaceVariant
                            )
                        }
                    }
                }
            }
        }
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
        // Category emoji avatar (Monzo-style), falls back to +/- for money in/out.
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
                text = CategoryIcons.emojiFor(transaction.category),
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
                text = buildString {
                    append(CategoryIcons.labelFor(transaction.category))
                    if (transaction.note.isNotBlank()) {
                        append(" · ")
                        append(transaction.note)
                    }
                },
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )
        }
        Text(
            text = transaction.amountMoney().formatted(),
            style = MaterialTheme.typography.bodyLarge,
            fontWeight = FontWeight.SemiBold,
            color = if (transaction.isCredit) NexoraPrimary else MaterialTheme.colorScheme.error
        )
    }
}
