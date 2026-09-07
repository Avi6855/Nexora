package com.nexora.app.feature.disputes

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import com.nexora.app.core.design.component.NexoraCard
import com.nexora.app.core.design.component.NexoraTopBar
import com.nexora.app.core.design.theme.NexoraPrimary
import com.nexora.app.core.model.disputeReasonOptions

/**
 * ReportDisputeScreen is "report a problem with this transaction": the reason
 * picker, optional description, and the immediate eligibility verdict.
 */
@Composable
fun ReportDisputeScreen(
    entryId: String,
    onNavigateBack: () -> Unit,
    viewModel: ReportDisputeViewModel = hiltViewModel()
) {
    val uiState by viewModel.uiState.collectAsState()
    var reason by remember { mutableStateOf<String?>(null) }
    var description by remember { mutableStateOf("") }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
    ) {
        NexoraTopBar(title = "Report a problem", onBackClick = onNavigateBack)

        when (val state = uiState) {
            is ReportDisputeUiState.Editing -> Column(
                modifier = Modifier
                    .fillMaxSize()
                    .verticalScroll(rememberScrollState())
                    .padding(16.dp),
                verticalArrangement = Arrangement.spacedBy(16.dp)
            ) {
                NexoraCard {
                    Column(
                        modifier = Modifier.padding(16.dp),
                        verticalArrangement = Arrangement.spacedBy(6.dp)
                    ) {
                        Text(
                            text = "What went wrong?",
                            style = MaterialTheme.typography.titleMedium,
                            fontWeight = FontWeight.SemiBold
                        )
                        Text(
                            text = "We'll raise a case with the merchant's scheme when eligible. " +
                                "Card payments and direct debits qualify; transfers are covered " +
                                "by our scam protection instead.",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant
                        )
                    }
                }

                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    disputeReasonOptions.forEach { (value, label) ->
                        val selected = reason == value
                        Row(
                            modifier = Modifier
                                .fillMaxWidth()
                                .clip(RoundedCornerShape(12.dp))
                                .background(
                                    if (selected) NexoraPrimary.copy(alpha = 0.10f)
                                    else MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.5f)
                                )
                                .border(
                                    width = if (selected) 2.dp else 1.dp,
                                    color = if (selected) NexoraPrimary
                                    else MaterialTheme.colorScheme.outlineVariant,
                                    shape = RoundedCornerShape(12.dp)
                                )
                                .clickable { reason = value }
                                .padding(14.dp)
                        ) {
                            Text(
                                text = label,
                                style = MaterialTheme.typography.bodyMedium,
                                fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Normal,
                                color = if (selected) NexoraPrimary
                                else MaterialTheme.colorScheme.onSurface
                            )
                        }
                    }
                }

                OutlinedTextField(
                    value = description,
                    onValueChange = { description = it.take(500) },
                    modifier = Modifier.fillMaxWidth(),
                    placeholder = { Text("Tell us what happened (optional)") },
                    minLines = 3
                )

                TextButton(
                    onClick = { reason?.let { viewModel.submit(entryId, it, description) } },
                    enabled = reason != null,
                    modifier = Modifier.align(Alignment.End)
                ) {
                    Text("Raise dispute")
                }
            }

            is ReportDisputeUiState.Submitting -> Column(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(32.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.Center
            ) {
                CircularProgressIndicator(color = NexoraPrimary)
                Spacer(modifier = Modifier.height(16.dp))
                Text(
                    "Running eligibility checks…",
                    style = MaterialTheme.typography.bodyLarge
                )
            }

            is ReportDisputeUiState.Done -> Column(
                modifier = Modifier
                    .fillMaxSize()
                    .verticalScroll(rememberScrollState())
                    .padding(16.dp),
                verticalArrangement = Arrangement.spacedBy(16.dp)
            ) {
                val case = state.case
                NexoraCard {
                    Column(
                        modifier = Modifier.padding(16.dp),
                        verticalArrangement = Arrangement.spacedBy(8.dp)
                    ) {
                        Text(
                            text = if (case.eligibility.eligible) "Case raised 🎉" else "Not eligible",
                            style = MaterialTheme.typography.titleLarge,
                            fontWeight = FontWeight.Bold,
                            color = if (case.eligibility.eligible) NexoraPrimary
                            else MaterialTheme.colorScheme.error
                        )
                        Text(
                            text = case.eligibility.reason,
                            style = MaterialTheme.typography.bodyMedium
                        )
                        if (case.eligibility.eligible) {
                            Text(
                                text = stageLabel(case.stage),
                                style = MaterialTheme.typography.bodyMedium,
                                fontWeight = FontWeight.SemiBold
                            )
                            if (case.provisionalCredit) {
                                Text(
                                    text = "Provisional credit: the amount is temporarily credited " +
                                        "while the scheme investigates.",
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant
                                )
                            }
                            case.deadline?.let {
                                Text(
                                    text = "Next deadline: $it",
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant
                                )
                            }
                        }
                    }
                }
                TextButton(onClick = onNavigateBack, modifier = Modifier.align(Alignment.End)) {
                    Text("Done")
                }
            }

            is ReportDisputeUiState.Error -> Column(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(24.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.Center
            ) {
                Text(
                    text = state.message,
                    style = MaterialTheme.typography.bodyLarge,
                    color = MaterialTheme.colorScheme.error
                )
                Spacer(modifier = Modifier.height(12.dp))
                TextButton(onClick = onNavigateBack) { Text("Go back") }
            }
        }
    }
}
