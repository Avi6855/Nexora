package com.nexora.app.feature.payments

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.nexora.app.core.design.component.NexoraButton
import com.nexora.app.core.design.component.NexoraButtonStyle
import com.nexora.app.core.design.component.NexoraCard
import com.nexora.app.core.design.component.NexoraTopBar
import java.util.UUID

@Composable
fun ReviewPaymentScreen(
    recipientName: String,
    sortCode: String,
    accountNumber: String,
    amount: String,
    reference: String,
    onNavigateBack: () -> Unit,
    onNavigateToProcessing: (String) -> Unit
) {
    val amountPence = amount.toDoubleOrNull()?.times(100)?.toLong() ?: 0L
    val formattedAmount = "\u00A3${amount}"

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
    ) {
        NexoraTopBar(
            title = "Review Payment",
            onBackClick = onNavigateBack
        )

        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp)
        ) {
            NexoraCard {
                Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    ReviewRow(label = "Recipient", value = recipientName)
                    ReviewRow(label = "Sort Code", value = sortCode)
                    ReviewRow(label = "Account", value = accountNumber)
                    ReviewRow(label = "Amount", value = formattedAmount)
                    if (reference.isNotEmpty()) {
                        ReviewRow(label = "Reference", value = reference)
                    }
                }
            }

            NexoraCard {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    ReviewRow(label = "Fee", value = "\u00A30.00")
                    ReviewRow(
                        label = "Total",
                        value = formattedAmount,
                        valueFontWeight = FontWeight.Bold
                    )
                }
            }

            Spacer(modifier = Modifier.weight(1f))

            NexoraButton(
                onClick = {
                    val idempotencyKey = UUID.randomUUID().toString()
                    onNavigateToProcessing(idempotencyKey)
                },
                text = "Confirm & Send"
            )

            Spacer(modifier = Modifier.height(8.dp))

            NexoraButton(
                onClick = onNavigateBack,
                text = "Cancel",
                style = NexoraButtonStyle.Secondary
            )
        }
    }
}

@Composable
private fun ReviewRow(
    label: String,
    value: String,
    valueFontWeight: FontWeight = FontWeight.Medium
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .semantics {
                contentDescription = "$label: $value"
            },
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant
        )
        Text(
            text = value,
            style = MaterialTheme.typography.bodyMedium,
            fontWeight = valueFontWeight
        )
    }
}
