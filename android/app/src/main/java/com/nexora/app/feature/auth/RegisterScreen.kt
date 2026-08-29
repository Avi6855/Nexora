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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
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
import com.nexora.app.core.design.component.NexoraTextField
import com.nexora.app.core.design.component.NexoraTopBar
import com.nexora.app.core.design.theme.NexoraPrimary

@Composable
fun RegisterScreen(
    onNavigateToLogin: () -> Unit,
    onNavigateToOtp: (String) -> Unit,
    viewModel: RegisterViewModel = hiltViewModel()
) {
    val uiState by viewModel.uiState.collectAsState()
    val email by viewModel.email.collectAsState()
    val password by viewModel.password.collectAsState()
    val firstName by viewModel.firstName.collectAsState()
    val lastName by viewModel.lastName.collectAsState()
    val phoneNumber by viewModel.phoneNumber.collectAsState()
    val confirmPassword by viewModel.confirmPassword.collectAsState()
    var contentVisible by remember { mutableStateOf(false) }

    LaunchedEffect(Unit) {
        contentVisible = true
    }

    when (val state = uiState) {
        is RegisterUiState.Success -> {
            onNavigateToOtp(state.email)
        }
        else -> {}
    }

    Scaffold(
        topBar = {
            NexoraTopBar(
                title = "",
                onBackClick = onNavigateToLogin
            )
        },
        containerColor = MaterialTheme.colorScheme.background
    ) { paddingValues ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(paddingValues)
                .padding(24.dp)
                .verticalScroll(rememberScrollState()),
            horizontalAlignment = Alignment.CenterHorizontally
        ) {
            Spacer(modifier = Modifier.height(16.dp))

            AnimatedVisibility(
                visible = contentVisible,
                enter = fadeIn() + slideInVertically()
            ) {
                Column(
                    horizontalAlignment = Alignment.CenterHorizontally
                ) {
                    Text(
                        text = "Create Account",
                        style = MaterialTheme.typography.displaySmall,
                        fontWeight = FontWeight.Bold,
                        modifier = Modifier.semantics {
                            contentDescription = "Create your Nexora account"
                        }
                    )

                Spacer(modifier = Modifier.height(8.dp))

                Text(
                    text = "Start your banking journey",
                    style = MaterialTheme.typography.bodyLarge,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )

                Spacer(modifier = Modifier.height(32.dp))

                NexoraTextField(
                    value = firstName,
                    onValueChange = viewModel::onFirstNameChange,
                    label = "First Name",
                    placeholder = "Enter your first name"
                )

                Spacer(modifier = Modifier.height(12.dp))

                NexoraTextField(
                    value = lastName,
                    onValueChange = viewModel::onLastNameChange,
                    label = "Last Name",
                    placeholder = "Enter your last name"
                )

                Spacer(modifier = Modifier.height(12.dp))

                NexoraTextField(
                    value = email,
                    onValueChange = viewModel::onEmailChange,
                    label = "Email",
                    placeholder = "Enter your email",
                    keyboardType = androidx.compose.ui.text.input.KeyboardType.Email
                )

                Spacer(modifier = Modifier.height(12.dp))

                NexoraTextField(
                    value = phoneNumber,
                    onValueChange = viewModel::onPhoneNumberChange,
                    label = "Phone Number",
                    placeholder = "+44 7700 900000",
                    keyboardType = androidx.compose.ui.text.input.KeyboardType.Phone,
                    helperText = "We'll send a verification code"
                )

                Spacer(modifier = Modifier.height(12.dp))

                NexoraTextField(
                    value = password,
                    onValueChange = viewModel::onPasswordChange,
                    label = "Password",
                    placeholder = "Create a password",
                    isPassword = true,
                    helperText = "At least 8 characters"
                )

                Spacer(modifier = Modifier.height(12.dp))

                NexoraTextField(
                    value = confirmPassword,
                    onValueChange = viewModel::onConfirmPasswordChange,
                    label = "Confirm Password",
                    placeholder = "Confirm your password",
                    isPassword = true,
                    isError = confirmPassword.isNotEmpty() && confirmPassword != password,
                    errorMessage = if (confirmPassword.isNotEmpty() && confirmPassword != password) "Passwords don't match" else ""
                )

                Spacer(modifier = Modifier.height(24.dp))

                NexoraButton(
                    onClick = viewModel::register,
                    text = "Create Account",
                    loading = uiState is RegisterUiState.Loading
                )

                Spacer(modifier = Modifier.height(12.dp))

                TextButton(
                    onClick = onNavigateToLogin,
                    modifier = Modifier.fillMaxWidth()
                ) {
                    Text(
                        text = "Already have an account? Sign In",
                        color = NexoraPrimary,
                        style = MaterialTheme.typography.bodyMedium,
                        fontWeight = FontWeight.Medium
                    )
                }

                if (uiState is RegisterUiState.Error) {
                    Spacer(modifier = Modifier.height(12.dp))
                    Text(
                        text = (uiState as RegisterUiState.Error).message,
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
}
