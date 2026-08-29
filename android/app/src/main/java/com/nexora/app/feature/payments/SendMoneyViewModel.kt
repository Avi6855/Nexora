package com.nexora.app.feature.payments

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.nexora.app.core.model.Payment
import com.nexora.app.core.network.api.PaymentApi
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed class SendMoneyUiState {
    data object Idle : SendMoneyUiState()
    data object Loading : SendMoneyUiState()
    data class Success(val payment: Payment) : SendMoneyUiState()
    data class Error(val message: String) : SendMoneyUiState()
}

@HiltViewModel
class SendMoneyViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val paymentApi: PaymentApi
) : ViewModel() {

    private val accountId: String = savedStateHandle["accountId"] ?: ""

    private val _uiState = MutableStateFlow<SendMoneyUiState>(SendMoneyUiState.Idle)
    val uiState: StateFlow<SendMoneyUiState> = _uiState.asStateFlow()

    private val _recipientName = MutableStateFlow("")
    val recipientName: StateFlow<String> = _recipientName.asStateFlow()

    private val _sortCode = MutableStateFlow("")
    val sortCode: StateFlow<String> = _sortCode.asStateFlow()

    private val _accountNumber = MutableStateFlow("")
    val accountNumber: StateFlow<String> = _accountNumber.asStateFlow()

    private val _amount = MutableStateFlow("")
    val amount: StateFlow<String> = _amount.asStateFlow()

    private val _reference = MutableStateFlow("")
    val reference: StateFlow<String> = _reference.asStateFlow()

    fun onRecipientNameChange(value: String) { _recipientName.value = value }
    fun onSortCodeChange(value: String) { _sortCode.value = value }
    fun onAccountNumberChange(value: String) { _accountNumber.value = value }
    fun onAmountChange(value: String) { _amount.value = value }
    fun onReferenceChange(value: String) { _reference.value = value }

    fun createPayment() {
        if (_recipientName.value.isBlank() || _sortCode.value.isBlank() ||
            _accountNumber.value.isBlank() || _amount.value.isBlank()) {
            _uiState.value = SendMoneyUiState.Error("Please fill in all required fields")
            return
        }

        val amountPence = _amount.value.toDoubleOrNull()
        if (amountPence == null || amountPence <= 0) {
            _uiState.value = SendMoneyUiState.Error("Please enter a valid amount")
            return
        }

        viewModelScope.launch {
            _uiState.value = SendMoneyUiState.Loading
            try {
                val response = paymentApi.createPayment(
                    com.nexora.app.core.network.api.CreatePaymentRequest(
                        accountId = accountId,
                        amount = (amountPence * 100).toLong(),
                        recipientName = _recipientName.value,
                        recipientAccountNumber = _accountNumber.value,
                        recipientSortCode = _sortCode.value,
                        reference = _reference.value
                    )
                )
                if (response.isSuccess && response.data != null) {
                    _uiState.value = SendMoneyUiState.Success(response.data)
                } else {
                    _uiState.value = SendMoneyUiState.Error(response.error ?: "Payment failed")
                }
            } catch (e: Exception) {
                _uiState.value = SendMoneyUiState.Error(e.message ?: "Network error occurred")
            }
        }
    }
}
