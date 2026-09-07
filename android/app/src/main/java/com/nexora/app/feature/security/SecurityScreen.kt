package com.nexora.app.feature.security

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
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
import com.nexora.app.core.design.component.NexoraTopBar
import com.nexora.app.core.design.theme.NexoraPrimary

@Composable
fun SecurityScreen(
    onNavigateBack: () -> Unit,
    onNavigateToDelegatedAccess: () -> Unit = {},
    viewModel: SecurityViewModel = hiltViewModel()
) {
    val biometricEnabled by viewModel.biometricEnabled.collectAsState()
    val lockdownEnabled by viewModel.lockdownEnabled.collectAsState()
    val lockdownBusy by viewModel.lockdownBusy.collectAsState()
    val lockdownError by viewModel.lockdownError.collectAsState()

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
    ) {
        NexoraTopBar(
            title = "Security",
            onBackClick = onNavigateBack
        )

        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp)
        ) {
            Text(
                text = "Biometric Authentication",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold
            )

            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(12.dp))
                    .background(MaterialTheme.colorScheme.surface)
                    .padding(16.dp)
                    .semantics {
                        contentDescription = "Biometric authentication toggle, ${if (biometricEnabled) "enabled" else "disabled"}"
                    },
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Column(modifier = Modifier.weight(1f)) {
                    Text(
                        text = "Use fingerprint or face ID",
                        style = MaterialTheme.typography.bodyLarge
                    )
                    Text(
                        text = "Quickly and securely access your account",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
                Switch(
                    checked = biometricEnabled,
                    onCheckedChange = { viewModel.toggleBiometric() },
                    colors = SwitchDefaults.colors(
                        checkedThumbColor = NexoraPrimary,
                        checkedTrackColor = NexoraPrimary.copy(alpha = 0.3f)
                    )
                )
            }

            Spacer(modifier = Modifier.height(8.dp))

            Text(
                text = "Emergency Lockdown",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold
            )

            // Emergency lockdown (Monzo-style): one tap blocks every way
            // money can leave the account — card payments, transfers, cash —
            // while money in, Direct Debits and savings sweeps continue.
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(12.dp))
                    .background(
                        if (lockdownEnabled) MaterialTheme.colorScheme.errorContainer
                        else MaterialTheme.colorScheme.surface
                    )
                    .padding(16.dp)
                    .semantics {
                        contentDescription = "Emergency lockdown, ${if (lockdownEnabled) "enabled" else "disabled"}"
                    },
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Column(modifier = Modifier.weight(1f)) {
                    Text(
                        text = if (lockdownEnabled) "Account is LOCKED" else "Freeze everything",
                        style = MaterialTheme.typography.bodyLarge,
                        fontWeight = FontWeight.SemiBold
                    )
                    Text(
                        text = if (lockdownEnabled)
                            "Card payments, transfers and cash withdrawals are blocked. Money in still works."
                        else
                            "Instantly block all money out: cards, transfers, cash. Money in stays on.",
                        style = MaterialTheme.typography.bodySmall,
                        color = if (lockdownEnabled) MaterialTheme.colorScheme.onErrorContainer
                        else MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
                Switch(
                    checked = lockdownEnabled,
                    enabled = !lockdownBusy,
                    onCheckedChange = { viewModel.toggleLockdown() },
                    colors = SwitchDefaults.colors(
                        checkedThumbColor = MaterialTheme.colorScheme.error,
                        checkedTrackColor = MaterialTheme.colorScheme.error.copy(alpha = 0.3f)
                    )
                )
            }

            lockdownError?.let { message ->
                Text(
                    text = message,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier.padding(horizontal = 4.dp)
                )
            }

            Spacer(modifier = Modifier.height(8.dp))

            Text(
                text = "App Security",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold
            )

            SecurityItem(
                title = "Change PIN",
                subtitle = "Update your 4-digit PIN"
            )

            SecurityItem(
                title = "Change Password",
                subtitle = "Update your account password"
            )

            SecurityItem(
                title = "Two-Factor Authentication",
                subtitle = "Manage 2FA settings"
            )

            SecurityItem(
                title = "Login History",
                subtitle = "View recent login activity"
            )

            SecurityItem(
                title = "Devices",
                subtitle = "Manage connected devices"
            )

            // Delegated access / Consent Centre: time-bound, capability-scoped
            // view-only access for people you trust (never money movement).
            SecurityItem(
                title = "Delegated access",
                subtitle = "Share view-only access, time-limited",
                onClick = onNavigateToDelegatedAccess
            )
        }
    }
}

@Composable
private fun SecurityItem(
    title: String,
    subtitle: String,
    onClick: () -> Unit = {}
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(12.dp))
            .clickable(onClick = onClick)
            .padding(16.dp)
            .semantics {
                contentDescription = "$title. $subtitle"
            },
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = title,
                style = MaterialTheme.typography.bodyLarge,
                fontWeight = FontWeight.Medium
            )
            Text(
                text = subtitle,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )
        }
        Icon(
            imageVector = Icons.AutoMirrored.Filled.KeyboardArrowRight,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onSurfaceVariant
        )
    }
}
