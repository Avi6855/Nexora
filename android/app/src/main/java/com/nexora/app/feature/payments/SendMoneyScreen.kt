package com.nexora.app.feature.payments

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import com.nexora.app.core.design.component.NexoraButton
import com.nexora.app.core.design.component.NexoraCard
import com.nexora.app.core.design.component.NexoraTextField
import com.nexora.app.core.design.component.NexoraTopBar
import com.nexora.app.core.design.theme.NexoraPrimary

@Composable
fun SendMoneyScreen(
    accountId: String,
    onNavigateBack: () -> Unit,
    onNavigateToReview: () -> Unit,
    viewModel: SendMoneyViewModel = hiltViewModel()
) {
    val uiState by viewModel.uiState.collectAsState()
    val recipientName by viewModel.recipientName.collectAsState()
    val sortCode by viewModel.sortCode.collectAsState()
    val accountNumber by viewModel.accountNumber.collectAsState()
    val amount by viewModel.amount.collectAsState()
    val reference by viewModel.reference.collectAsState()

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
    ) {
        NexoraTopBar(
            title = "Send Money",
            onBackClick = onNavigateBack
        )

        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp)
        ) {
            Text(
                text = "Recipient Details",
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.SemiBold
            )

            NexoraCard {
                Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    NexoraTextField(
                        value = recipientName,
                        onValueChange = viewModel::onRecipientNameChange,
                        label = "Recipient Name",
                        placeholder = "John Smith"
                    )

                    NexoraTextField(
                        value = sortCode,
                        onValueChange = viewModel::onSortCodeChange,
                        label = "Sort Code",
                        placeholder = "00-00-00",
                        keyboardType = KeyboardType.Number
                    )

                    NexoraTextField(
                        value = accountNumber,
                        onValueChange = viewModel::onAccountNumberChange,
                        label = "Account Number",
                        placeholder = "12345678",
                        keyboardType = KeyboardType.Number
                    )
                }
            }

            Text(
                text = "Payment Details",
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.SemiBold
            )

            NexoraCard {
                Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    NexoraTextField(
                        value = amount,
                        onValueChange = viewModel::onAmountChange,
                        label = "Amount (\u00A3)",
                        placeholder = "0.00",
                        keyboardType = KeyboardType.Decimal
                    )

                    NexoraTextField(
                        value = reference,
                        onValueChange = viewModel::onReferenceChange,
                        label = "Reference (optional)",
                        placeholder = "Dinner, Rent, etc.",
                        helperText = "This helps the recipient identify the payment"
                    )
                }
            }

            if (uiState is SendMoneyUiState.Error) {
                Text(
                    text = (uiState as SendMoneyUiState.Error).message,
                    color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodySmall
                )
            }

            Spacer(modifier = Modifier.height(8.dp))

            NexoraButton(
                onClick = viewModel::createPayment,
                text = "Review Payment",
                loading = uiState is SendMoneyUiState.Loading
            )
        }
    }
}
