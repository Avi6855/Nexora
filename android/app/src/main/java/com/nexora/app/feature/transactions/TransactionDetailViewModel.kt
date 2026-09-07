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

    private val _noteSaving = MutableStateFlow(false)
    val noteSaving: StateFlow<Boolean> = _noteSaving.asStateFlow()

    private val _noteSaved = MutableStateFlow(false)
    val noteSaved: StateFlow<Boolean> = _noteSaved.asStateFlow()

    init {
        loadTransaction()
    }

    fun loadTransaction() {
        viewModelScope.launch {
            _uiState.value = TransactionDetailUiState.Loading
            try {
                val transaction = transferApi.getTransaction(transactionId)
                _uiState.value = TransactionDetailUiState.Success(transaction)
            } catch (e: Exception) {
                _uiState.value = TransactionDetailUiState.Error(e.message ?: "Network error occurred")
            }
        }
    }

    /** Saves the user's note on this transaction (empty string clears it). */
    fun saveNote(note: String) {
        if (transactionId.isBlank()) return
        viewModelScope.launch {
            _noteSaving.value = true
            _noteSaved.value = false
            try {
                transferApi.updateNote(transactionId, com.nexora.app.core.network.api.NoteRequest(note))
                val current = _uiState.value
                if (current is TransactionDetailUiState.Success) {
                    _uiState.value = current.copy(transaction = current.transaction.copy(note = note))
                }
                _noteSaved.value = true
            } catch (e: Exception) {
                _uiState.value = TransactionDetailUiState.Error(e.message ?: "Could not save note")
            } finally {
                _noteSaving.value = false
            }
        }
    }
}
