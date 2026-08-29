package com.nexora.app.feature.transactions

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.nexora.app.core.model.Transaction
import com.nexora.app.core.network.api.TransferApi
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed class TransactionDetailUiState {
    data object Loading : TransactionDetailUiState()
    data class Success(val transaction: Transaction) : TransactionDetailUiState()
    data class Error(val message: String) : TransactionDetailUiState()
}

@HiltViewModel
class TransactionDetailViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val transferApi: TransferApi
) : ViewModel() {

    private val transactionId: String = savedStateHandle["transactionId"] ?: ""

    private val _uiState = MutableStateFlow<TransactionDetailUiState>(TransactionDetailUiState.Loading)
    val uiState: StateFlow<TransactionDetailUiState> = _uiState.asStateFlow()

    init {
        loadTransaction()
    }

    fun loadTransaction() {
        viewModelScope.launch {
            _uiState.value = TransactionDetailUiState.Loading
            try {
                val response = transferApi.getTransaction(transactionId)
                if (response.isSuccess && response.data != null) {
                    _uiState.value = TransactionDetailUiState.Success(response.data)
                } else {
                    _uiState.value = TransactionDetailUiState.Error(response.error ?: "Failed to load transaction")
                }
            } catch (e: Exception) {
                _uiState.value = TransactionDetailUiState.Error(e.message ?: "Network error occurred")
            }
        }
    }
}
