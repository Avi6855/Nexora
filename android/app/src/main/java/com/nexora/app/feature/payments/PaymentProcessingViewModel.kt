package com.nexora.app.feature.payments

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.nexora.app.core.model.Payment
import com.nexora.app.core.model.PaymentState
import com.nexora.app.core.network.api.PaymentApi
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed class PaymentProcessingUiState {
    data object Processing : PaymentProcessingUiState()
    data class Success(val payment: Payment) : PaymentProcessingUiState()
    data class Failed(val payment: Payment) : PaymentProcessingUiState()
    data class Unknown(val payment: Payment) : PaymentProcessingUiState()
    data class Error(val message: String) : PaymentProcessingUiState()
}

@HiltViewModel
class PaymentProcessingViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val paymentApi: PaymentApi
) : ViewModel() {

    private val paymentId: String = savedStateHandle["paymentId"] ?: ""

    private val _uiState = MutableStateFlow<PaymentProcessingUiState>(PaymentProcessingUiState.Processing)
    val uiState: StateFlow<PaymentProcessingUiState> = _uiState.asStateFlow()

    init {
        pollPaymentStatus()
    }

    private fun pollPaymentStatus() {
        viewModelScope.launch {
            val maxAttempts = 15
            repeat(maxAttempts) {
                delay(2000)
                try {
                    val response = paymentApi.getPayment(paymentId)
                    if (response.isSuccess && response.data != null) {
                        when (response.data.state) {
                            PaymentState.SETTLED, PaymentState.CONFIRMED -> {
                                _uiState.value = PaymentProcessingUiState.Success(response.data)
                                return@launch
                            }
                            PaymentState.FAILED -> {
                                _uiState.value = PaymentProcessingUiState.Failed(response.data)
                                return@launch
                            }
                            PaymentState.UNKNOWN -> {
                                _uiState.value = PaymentProcessingUiState.Unknown(response.data)
                                return@launch
                            }
                            else -> { /* still processing, continue polling */ }
                        }
                    }
                } catch (_: Exception) {
                    // Network error, keep polling
                }
            }
            _uiState.value = PaymentProcessingUiState.Error("Payment timed out. Please check your account.")
        }
    }
}
