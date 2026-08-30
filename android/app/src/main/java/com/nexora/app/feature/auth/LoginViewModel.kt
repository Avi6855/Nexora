package com.nexora.app.feature.auth

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.nexora.app.core.network.api.AuthApi
import com.nexora.app.core.network.api.LoginRequest
import com.nexora.app.core.security.SecureTokenStorage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed class LoginUiState {
    data object Idle : LoginUiState()
    data object Loading : LoginUiState()
    data class Success(val email: String) : LoginUiState()
    data class Error(val message: String) : LoginUiState()
}

@HiltViewModel
class LoginViewModel @Inject constructor(
    private val authApi: AuthApi,
    private val tokenStorage: SecureTokenStorage
) : ViewModel() {

    private val _uiState = MutableStateFlow<LoginUiState>(LoginUiState.Idle)
    val uiState: StateFlow<LoginUiState> = _uiState.asStateFlow()

    private val _email = MutableStateFlow("")
    val email: StateFlow<String> = _email.asStateFlow()

    private val _password = MutableStateFlow("")
    val password: StateFlow<String> = _password.asStateFlow()

    fun onEmailChange(value: String) { _email.value = value }
    fun onPasswordChange(value: String) { _password.value = value }

    fun login() {
        if (_email.value.isBlank() || _password.value.isBlank()) {
            _uiState.value = LoginUiState.Error("Please fill in all fields")
            return
        }

        viewModelScope.launch {
            _uiState.value = LoginUiState.Loading
            try {
                val tokens = authApi.login(
                    LoginRequest(
                        email = _email.value,
                        password = _password.value,
                        deviceId = android.os.Build.MODEL
                    )
                )
                if (tokens.accessToken.isNotBlank()) {
                    tokenStorage.saveTokens(tokens.accessToken, tokens.refreshToken)
                    tokenStorage.saveUserEmail(_email.value)
                    _uiState.value = LoginUiState.Success(_email.value)
                } else {
                    _uiState.value = LoginUiState.Error("Login failed")
                }
            } catch (e: Exception) {
                _uiState.value = LoginUiState.Error(e.message ?: "Network error occurred")
            }
        }
    }
}
