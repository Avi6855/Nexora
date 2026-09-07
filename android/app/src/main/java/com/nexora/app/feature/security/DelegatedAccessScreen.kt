package com.nexora.app.feature.security

import androidx.compose.foundation.background
import androidx.compose.foundation.border
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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
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
import com.nexora.app.core.design.component.NexoraBadge
import com.nexora.app.core.design.component.NexoraCard
import com.nexora.app.core.design.component.BadgeStyle
import com.nexora.app.core.design.theme.NexoraPrimary
import com.nexora.app.core.model.DelegationGrant

private val scopeOptions = listOf(
    "VIEW_BALANCE" to "See your balance",
    "VIEW_TRANSACTIONS" to "See your transactions",
    "DOWNLOAD_STATEMENTS" to "Download statements"
)

private val durationOptions = listOf(7, 30, 90)

/**
 * DelegatedAccessScreen is the Consent Centre: time-bound, capability-scoped
 * access for people you trust (view-only babysitter access, accountant view).
 * Money movement is never delegable.
 */
@Composable
fun DelegatedAccessScreen(
    onNavigateBack: () -> Unit,
    viewModel: DelegatedAccessViewModel = hiltViewModel()
) {
    val uiState by viewModel.uiState.collectAsState()
    var showForm by remember { mutableStateOf(false) }
    var email by remember { mutableStateOf("") }
    var label by remember { mutableStateOf("") }
    var scopes by remember { mutableStateOf(setOf("VIEW_BALANCE")) }
    var days by remember { mutableStateOf(7) }

    Column(
        modifier = Modifier
            .fillMaxSize()
        .background(MaterialTheme.colorScheme.background)
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = onNavigateBack) {
                Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
            }
            Text(
                text = "Delegated access",
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold
            )
        }

        when {
            uiState.loading -> Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Text("Loading…", style = MaterialTheme.typography.bodyLarge)
            }
            uiState.error != null -> Column(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(24.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.Center
            ) {
                Text(
                    uiState.error ?: "",
                    style = MaterialTheme.typography.bodyLarge,
                    color = MaterialTheme.colorScheme.error
                )
                TextButton(onClick = { viewModel.loadData() }) { Text("Retry") }
            }
            else -> LazyColumn(
                modifier = Modifier.fillMaxSize(),
                contentPadding = androidx.compose.foundation.layout.PaddingValues(16.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp)
            ) {
                item {
                    Text(
                        "Give someone view-only access for a limited time. They can never move " +
                            "money — payments stay protected by your security controls.",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
                items(uiState.grants) { grant ->
                    GrantCard(grant = grant, onRevoke = { viewModel.revokeGrant(grant.grantId) })
                }
                item {
                    if (showForm) {
                        GrantForm(
                            email = email, label = label, scopes = scopes, days = days,
                            onEmailChange = { email = it },
                            onLabelChange = { label = it },
                            onScopeToggle = { s ->
                                scopes = if (s in scopes) scopes - s else scopes + s
                            },
                            onDaysChange = { days = it },
                            onSubmit = {
                                viewModel.createGrant(email, label, scopes, days)
                                showForm = false
                                email = ""; label = ""; scopes = setOf("VIEW_BALANCE"); days = 7
                            },
                            onCancel = { showForm = false }
                        )
                    } else {
                        Button(onClick = { showForm = true }, modifier = Modifier.fillMaxWidth()) {
                            Icon(Icons.Filled.Add, contentDescription = null)
                            Spacer(modifier = Modifier.width(6.dp))
                            Text("Share access")
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun GrantCard(grant: DelegationGrant, onRevoke: () -> Unit) {
    NexoraCard {
        Column(modifier = Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(
                    modifier = Modifier
                        .size(36.dp)
                        .clip(CircleShape)
                        .background(NexoraPrimary.copy(alpha = 0.12f)),
                    contentAlignment = Alignment.Center
                ) {
                    Text(
                        text = grant.label.ifEmpty { "?" }.firstOrNull()?.uppercase() ?: "?",
                        color = NexoraPrimary,
                        fontWeight = FontWeight.Bold,
                        style = MaterialTheme.typography.titleSmall
                    )
                }
                Spacer(modifier = Modifier.width(10.dp))
                Column(modifier = Modifier.weight(1f)) {
                    Text(
                        text = grant.label.ifEmpty { grant.delegateEmail },
                        style = MaterialTheme.typography.bodyLarge,
                        fontWeight = FontWeight.SemiBold
                    )
                    Text(
                        text = grant.delegateEmail,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
                NexoraBadge(
                    text = grant.status,
                    style = when (grant.status) {
                        "ACTIVE" -> BadgeStyle.Success
                        "EXPIRED" -> BadgeStyle.Warning
                        else -> BadgeStyle.Info
                    }
                )
            }
            Text(
                text = "Until ${grant.expiresAt.take(10)} · " +
                    grant.scopes.joinToString(", ") { scopeLabel(it) },
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )
            if (grant.isActive) {
                TextButton(onClick = onRevoke) {
                    Text("Revoke now", color = MaterialTheme.colorScheme.error)
                }
            }
        }
    }
}

@Composable
private fun GrantForm(
    email: String, label: String, scopes: Set<String>, days: Int,
    onEmailChange: (String) -> Unit,
    onLabelChange: (String) -> Unit,
    onScopeToggle: (String) -> Unit,
    onDaysChange: (Int) -> Unit,
    onSubmit: () -> Unit,
    onCancel: () -> Unit
) {
    NexoraCard {
        Column(
            modifier = Modifier.padding(12.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp)
        ) {
            OutlinedTextField(
                value = email,
                onValueChange = onEmailChange,
                label = { Text("Their email") },
                modifier = Modifier.fillMaxWidth()
            )
            OutlinedTextField(
                value = label,
                onValueChange = onLabelChange,
                label = { Text("Who is it for? (Partner, Accountant…)") },
                modifier = Modifier.fillMaxWidth()
            )
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                scopeOptions.forEach { (value, name) ->
                    val selected = value in scopes
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .clip(RoundedCornerShape(10.dp))
                            .background(
                                if (selected) NexoraPrimary.copy(alpha = 0.10f)
                                else MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.4f)
                            )
                            .border(
                                width = if (selected) 2.dp else 1.dp,
                                color = if (selected) NexoraPrimary
                                else MaterialTheme.colorScheme.outlineVariant,
                                shape = RoundedCornerShape(10.dp)
                            )
                            .clickable { onScopeToggle(value) }
                            .padding(10.dp)
                    ) {
                        Text(
                            text = name,
                            style = MaterialTheme.typography.bodyMedium,
                            color = if (selected) NexoraPrimary else MaterialTheme.colorScheme.onSurface
                        )
                    }
                }
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                durationOptions.forEach { d ->
                    val selected = d == days
                    TextButton(onClick = { onDaysChange(d) }) {
                        Text(
                            "${d}d",
                            fontWeight = if (selected) FontWeight.Bold else FontWeight.Normal,
                            color = if (selected) NexoraPrimary
                            else MaterialTheme.colorScheme.onSurfaceVariant
                        )
                    }
                }
            }
            Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                TextButton(onClick = onCancel) { Text("Cancel") }
                Button(
                    onClick = onSubmit,
                    enabled = email.contains("@") && scopes.isNotEmpty()
                ) { Text("Grant access") }
            }
        }
    }
}

private fun scopeLabel(scope: String): String =
    scopeOptions.firstOrNull { it.first == scope }?.second ?: scope.lowercase().replace('_', ' ')
