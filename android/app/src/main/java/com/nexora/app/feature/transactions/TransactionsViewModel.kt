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

sealed class TransactionsUiState {
    data object Loading : TransactionsUiState()
    data class Success(val transactions: List<Transaction>) : TransactionsUiState()
    data object Empty : TransactionsUiState()
    data class Error(val message: String) : TransactionsUiState()
}

@HiltViewModel
class TransactionsViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val transferApi: TransferApi
) : ViewModel() {

    private val accountId: String = savedStateHandle["accountId"] ?: ""

    private val _uiState = MutableStateFlow<TransactionsUiState>(TransactionsUiState.Loading)
    val uiState: StateFlow<TransactionsUiState> = _uiState.asStateFlow()

    init {
        loadTransactions()
    }

    fun loadTransactions() {
        viewModelScope.launch {
            _uiState.value = TransactionsUiState.Loading
            try {
                val response = transferApi.getTransactions(accountId)
                if (response.isSuccess && !response.data.isNullOrEmpty()) {
                    _uiState.value = TransactionsUiState.Success(response.data)
                } else if (response.data.isNullOrEmpty()) {
                    _uiState.value = TransactionsUiState.Empty
                } else {
                    _uiState.value = TransactionsUiState.Error(response.error ?: "Failed to load transactions")
                }
            } catch (e: Exception) {
                _uiState.value = TransactionsUiState.Error(e.message ?: "Network error occurred")
            }
        }
    }
}
