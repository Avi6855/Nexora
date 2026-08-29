package com.nexora.app.feature.auth

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.nexora.app.core.network.api.AuthApi
import com.nexora.app.core.network.api.RegisterRequest
import com.nexora.app.core.security.SecureTokenStorage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed class RegisterUiState {
    data object Idle : RegisterUiState()
    data object Loading : RegisterUiState()
    data class Success(val email: String) : RegisterUiState()
    data class Error(val message: String) : RegisterUiState()
}

@HiltViewModel
class RegisterViewModel @Inject constructor(
    private val authApi: AuthApi,
    private val tokenStorage: SecureTokenStorage
) : ViewModel() {

    private val _uiState = MutableStateFlow<RegisterUiState>(RegisterUiState.Idle)
    val uiState: StateFlow<RegisterUiState> = _uiState.asStateFlow()

    private val _email = MutableStateFlow("")
    val email: StateFlow<String> = _email.asStateFlow()

    private val _password = MutableStateFlow("")
    val password: StateFlow<String> = _password.asStateFlow()

    private val _firstName = MutableStateFlow("")
    val firstName: StateFlow<String> = _firstName.asStateFlow()

    private val _lastName = MutableStateFlow("")
    val lastName: StateFlow<String> = _lastName.asStateFlow()

    private val _phoneNumber = MutableStateFlow("")
    val phoneNumber: StateFlow<String> = _phoneNumber.asStateFlow()

    private val _confirmPassword = MutableStateFlow("")
    val confirmPassword: StateFlow<String> = _confirmPassword.asStateFlow()

    fun onEmailChange(value: String) { _email.value = value }
    fun onPasswordChange(value: String) { _password.value = value }
    fun onFirstNameChange(value: String) { _firstName.value = value }
    fun onLastNameChange(value: String) { _lastName.value = value }
    fun onPhoneNumberChange(value: String) { _phoneNumber.value = value }
    fun onConfirmPasswordChange(value: String) { _confirmPassword.value = value }

    fun register() {
        if (_firstName.value.isBlank() || _lastName.value.isBlank() ||
            _email.value.isBlank() || _password.value.isBlank()
        ) {
            _uiState.value = RegisterUiState.Error("Please fill in all required fields")
            return
        }

        if (_password.value != _confirmPassword.value) {
            _uiState.value = RegisterUiState.Error("Passwords don't match")
            return
        }

        if (_password.value.length < 8) {
            _uiState.value = RegisterUiState.Error("Password must be at least 8 characters")
            return
        }

        viewModelScope.launch {
            _uiState.value = RegisterUiState.Loading
            try {
                val response = authApi.register(
                    RegisterRequest(
                        email = _email.value,
                        password = _password.value,
                        firstName = _firstName.value,
                        lastName = _lastName.value,
                        phoneNumber = _phoneNumber.value,
                        deviceId = android.os.Build.MODEL
                    )
                )
                if (response.isSuccess && response.data != null) {
                    tokenStorage.saveTokens(response.data.accessToken, response.data.refreshToken)
                    _uiState.value = RegisterUiState.Success(_email.value)
                } else {
                    _uiState.value = RegisterUiState.Error(response.error ?: response.message ?: "Registration failed")
                }
            } catch (e: Exception) {
                _uiState.value = RegisterUiState.Error(e.message ?: "Network error occurred")
            }
        }
    }
}
