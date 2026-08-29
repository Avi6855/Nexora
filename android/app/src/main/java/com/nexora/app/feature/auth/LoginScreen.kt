package com.nexora.app.feature.auth

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.slideInVertically
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Fingerprint
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import com.nexora.app.core.design.component.NexoraButton
import com.nexora.app.core.design.component.NexoraCard
import com.nexora.app.core.design.component.NexoraTextField
import com.nexora.app.core.design.theme.NexoraPrimary

@Composable
fun LoginScreen(
    onNavigateToRegister: () -> Unit,
    onNavigateToOtp: (String) -> Unit,
    onNavigateToHome: () -> Unit,
    viewModel: LoginViewModel = hiltViewModel()
) {
    val uiState by viewModel.uiState.collectAsState()
    val email by viewModel.email.collectAsState()
    val password by viewModel.password.collectAsState()
    var contentVisible by remember { mutableStateOf(false) }

    LaunchedEffect(Unit) {
        contentVisible = true
    }

    when (val state = uiState) {
        is LoginUiState.Success -> {
            onNavigateToOtp(state.email)
        }
        else -> {}
    }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
            .padding(24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center
    ) {
        AnimatedVisibility(
            visible = contentVisible,
            enter = fadeIn() + slideInVertically()
        ) {
            Column(
                horizontalAlignment = Alignment.CenterHorizontally
            ) {
                Text(
                    text = "Nexora",
                    style = MaterialTheme.typography.displayMedium,
                    fontWeight = FontWeight.Bold,
                    color = NexoraPrimary,
                    modifier = Modifier.semantics {
                        contentDescription = "Nexora banking app"
                    }
                )

                Spacer(modifier = Modifier.height(8.dp))

                Text(
                    text = "Welcome back",
                    style = MaterialTheme.typography.headlineMedium,
                    fontWeight = FontWeight.SemiBold
                )

                Spacer(modifier = Modifier.height(4.dp))

                Text(
                    text = "Sign in to continue",
                    style = MaterialTheme.typography.bodyLarge,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )

                Spacer(modifier = Modifier.height(40.dp))

                NexoraTextField(
                    value = email,
                    onValueChange = viewModel::onEmailChange,
                    label = "Email",
                    placeholder = "Enter your email",
                    keyboardType = androidx.compose.ui.text.input.KeyboardType.Email,
                    isError = uiState is LoginUiState.Error,
                    errorMessage = if (uiState is LoginUiState.Error) (uiState as LoginUiState.Error).message else ""
                )

                Spacer(modifier = Modifier.height(16.dp))

                NexoraTextField(
                    value = password,
                    onValueChange = viewModel::onPasswordChange,
                    label = "Password",
                    placeholder = "Enter your password",
                    isPassword = true,
                    isError = uiState is LoginUiState.Error
                )

                Spacer(modifier = Modifier.height(24.dp))

                NexoraButton(
                    onClick = viewModel::login,
                    text = "Sign In",
                    loading = uiState is LoginUiState.Loading
                )

                Spacer(modifier = Modifier.height(16.dp))

                NexoraCard(
                    showBorder = true,
                    cornerRadius = 12.dp
                ) {
                    IconButton(
                        onClick = { /* Biometric login */ },
                        modifier = Modifier
                            .fillMaxWidth()
                            .size(48.dp)
                            .semantics {
                                contentDescription = "Sign in with biometrics"
                            }
                    ) {
                        Icon(
                            imageVector = Icons.Filled.Fingerprint,
                            contentDescription = "Biometric login",
                            tint = NexoraPrimary,
                            modifier = Modifier.size(32.dp)
                        )
                    }
                }

                Spacer(modifier = Modifier.height(16.dp))

                TextButton(
                    onClick = onNavigateToRegister,
                    modifier = Modifier.fillMaxWidth()
                ) {
                    Text(
                        text = "Don't have an account? Sign Up",
                        color = NexoraPrimary,
                        style = MaterialTheme.typography.bodyMedium,
                        fontWeight = FontWeight.Medium
                    )
                }

                if (uiState is LoginUiState.Error) {
                    Spacer(modifier = Modifier.height(16.dp))
                    Text(
                        text = (uiState as LoginUiState.Error).message,
                        color = MaterialTheme.colorScheme.error,
                        style = MaterialTheme.typography.bodySmall,
                        textAlign = TextAlign.Center,
                        modifier = Modifier.fillMaxWidth()
                    )
                }
            }
        }
    }
}
