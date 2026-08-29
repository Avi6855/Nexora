package com.nexora.app.feature.auth

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.nexora.app.core.network.api.AuthApi
import com.nexora.app.core.network.api.OtpRequest
import com.nexora.app.core.network.api.ResendOtpRequest
import com.nexora.app.core.security.SecureTokenStorage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed class OtpUiState {
    data object Idle : OtpUiState()
    data object Loading : OtpUiState()
    data object Success : OtpUiState()
    data class Error(val message: String) : OtpUiState()
}

@HiltViewModel
class OtpViewModel @Inject constructor(
    private val authApi: AuthApi,
    private val tokenStorage: SecureTokenStorage
) : ViewModel() {

    private val _uiState = MutableStateFlow<OtpUiState>(OtpUiState.Idle)
    val uiState: StateFlow<OtpUiState> = _uiState.asStateFlow()

    private val _otp = MutableStateFlow("")
    val otp: StateFlow<String> = _otp.asStateFlow()

    fun onOtpChange(value: String) { _otp.value = value }

    fun verifyOtp(email: String) {
        if (_otp.value.isBlank()) {
            _uiState.value = OtpUiState.Error("Please enter the OTP")
            return
        }

        if (_otp.value.length != 6) {
            _uiState.value = OtpUiState.Error("Please enter a 6-digit code")
            return
        }

        viewModelScope.launch {
            _uiState.value = OtpUiState.Loading
            try {
                val response = authApi.verifyOtp(
                    OtpRequest(
                        email = email,
                        otp = _otp.value,
                        deviceId = android.os.Build.MODEL
                    )
                )
                if (response.isSuccess && response.data != null) {
                    tokenStorage.saveTokens(response.data.accessToken, response.data.refreshToken)
                    _uiState.value = OtpUiState.Success
                } else {
                    _uiState.value = OtpUiState.Error(response.error ?: response.message ?: "Verification failed")
                }
            } catch (e: Exception) {
                _uiState.value = OtpUiState.Error(e.message ?: "Network error occurred")
            }
        }
    }

    fun resendOtp(email: String) {
        viewModelScope.launch {
            try {
                authApi.resendOtp(ResendOtpRequest(email = email))
            } catch (_: Exception) {
                // Silently fail - user can try again
            }
        }
    }
}
