package com.nexora.app.feature.profile

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.slideInVertically
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
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ExitToApp
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.DarkMode
import androidx.compose.material.icons.filled.Group
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.MonitorHeart
import androidx.compose.material.icons.filled.ReportProblem
import androidx.compose.material.icons.filled.Notifications
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
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
import com.nexora.app.core.design.component.NexoraDialog
import com.nexora.app.core.design.component.NexoraTopBar
import com.nexora.app.core.design.theme.NexoraPrimary
import com.nexora.app.core.model.User

@Composable
fun ProfileScreen(
    onNavigateToSecurity: () -> Unit,
    onNavigateToDelegatedAccess: () -> Unit,
    onNavigateToDisputes: () -> Unit,
    onNavigateToSystemStatus: () -> Unit,
    onNavigateToLogin: () -> Unit,
    viewModel: ProfileViewModel = hiltViewModel()
) {
    val uiState by viewModel.uiState.collectAsState()
    var showLogoutDialog = remember { mutableStateOf(false) }
    var darkModeEnabled by remember { mutableStateOf(false) }

    if (showLogoutDialog.value) {
        NexoraDialog(
            onDismiss = { showLogoutDialog.value = false },
            title = "Logout",
            message = "Are you sure you want to logout?",
            confirmText = "Logout",
            onConfirm = {
                viewModel.logout()
                onNavigateToLogin()
            },
            dismissText = "Cancel"
        )
    }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
    ) {
        NexoraTopBar(title = "Profile")

        when (val state = uiState) {
            is ProfileUiState.Loading -> {
                Box(
                    modifier = Modifier.fillMaxSize(),
                    contentAlignment = Alignment.Center
                ) {
                    CircularProgressIndicator(color = NexoraPrimary)
                }
            }
            is ProfileUiState.Success -> {
                ProfileContent(
                    user = state.user,
                    onNavigateToSecurity = onNavigateToSecurity,
                    onNavigateToDelegatedAccess = onNavigateToDelegatedAccess,
                    onNavigateToDisputes = onNavigateToDisputes,
                    onNavigateToSystemStatus = onNavigateToSystemStatus,
                    onLogout = { showLogoutDialog.value = true }
                )
            }
            is ProfileUiState.Error -> {
                Box(
                    modifier = Modifier.fillMaxSize(),
                    contentAlignment = Alignment.Center
                ) {
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

@Composable
private fun ProfileContent(
    user: User,
    onNavigateToSecurity: () -> Unit,
    onNavigateToDelegatedAccess: () -> Unit,
    onNavigateToDisputes: () -> Unit,
    onNavigateToSystemStatus: () -> Unit,
    onLogout: () -> Unit
) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp)
    ) {
        AnimatedVisibility(
            visible = true,
            enter = fadeIn() + slideInVertically()
        ) {
            Column(
                modifier = Modifier.fillMaxWidth(),
                horizontalAlignment = Alignment.CenterHorizontally
            ) {
                Box(
                    modifier = Modifier
                        .size(80.dp)
                        .clip(CircleShape)
                        .background(NexoraPrimary.copy(alpha = 0.12f)),
                    contentAlignment = Alignment.Center
                ) {
                    Text(
                        text = user.initials,
                        style = MaterialTheme.typography.headlineLarge,
                        fontWeight = FontWeight.Bold,
                        color = NexoraPrimary
                    )
                }
                Spacer(modifier = Modifier.height(12.dp))
                Text(
                    text = user.fullName.ifEmpty { "User" },
                    style = MaterialTheme.typography.headlineSmall,
                    fontWeight = FontWeight.Bold
                )
                Text(
                    text = user.email,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
        }

        HorizontalDivider()

        ProfileMenuItem(
            icon = Icons.Filled.Lock,
            title = "Security",
            onClick = onNavigateToSecurity
        )

        ProfileMenuItem(
            icon = Icons.Filled.Group,
            title = "Delegated access",
            onClick = onNavigateToDelegatedAccess
        )

        ProfileMenuItem(
            icon = Icons.Filled.ReportProblem,
            title = "Disputes",
            onClick = onNavigateToDisputes
        )

        ProfileMenuItem(
            icon = Icons.Filled.MonitorHeart,
            title = "System status",
            onClick = onNavigateToSystemStatus
        )

        ProfileMenuItem(
            icon = Icons.Filled.Notifications,
            title = "Notifications",
            onClick = { }
        )

        HorizontalDivider()

        ProfileMenuItem(
            icon = Icons.AutoMirrored.Filled.ExitToApp,
            title = "Logout",
            onClick = onLogout,
            isDestructive = true
        )
    }
}

@Composable
private fun ProfileMenuItem(
    icon: androidx.compose.ui.graphics.vector.ImageVector,
    title: String,
    onClick: () -> Unit,
    isDestructive: Boolean = false
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(MaterialTheme.shapes.medium)
            .clickable(onClick = onClick)
            .padding(16.dp)
            .semantics {
                contentDescription = title
            },
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(16.dp)
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = if (isDestructive) MaterialTheme.colorScheme.error
            else MaterialTheme.colorScheme.onSurface
        )
        Text(
            text = title,
            style = MaterialTheme.typography.bodyLarge,
            modifier = Modifier.weight(1f),
            color = if (isDestructive) MaterialTheme.colorScheme.error
            else MaterialTheme.colorScheme.onSurface
        )
        Icon(
            imageVector = Icons.AutoMirrored.Filled.KeyboardArrowRight,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onSurfaceVariant
        )
    }
}
