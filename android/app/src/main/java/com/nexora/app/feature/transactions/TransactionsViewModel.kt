package com.nexora.app.feature.transactions

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.nexora.app.core.model.Transaction
import com.nexora.app.core.network.api.TransferApi
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.FlowPreview
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.debounce
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed class TransactionsUiState {
    data object Loading : TransactionsUiState()
    data class Success(val transactions: List<Transaction>) : TransactionsUiState()
    data object Empty : TransactionsUiState()
    data class Error(val message: String) : TransactionsUiState()
}

@HiltViewModel
@OptIn(FlowPreview::class)
class TransactionsViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val transferApi: TransferApi
) : ViewModel() {

    private val accountId: String = savedStateHandle["accountId"] ?: ""

    private val _uiState = MutableStateFlow<TransactionsUiState>(TransactionsUiState.Loading)
    val uiState: StateFlow<TransactionsUiState> = _uiState.asStateFlow()

    private val _searchQuery = MutableStateFlow("")
    val searchQuery: StateFlow<String> = _searchQuery.asStateFlow()

    private val _selectedCategory = MutableStateFlow<String?>(null)
    val selectedCategory: StateFlow<String?> = _selectedCategory.asStateFlow()

    private val _selectedType = MutableStateFlow<String?>(null)
    val selectedType: StateFlow<String?> = _selectedType.asStateFlow()

    private val _isFiltering = MutableStateFlow(false)
    val isFiltering: StateFlow<Boolean> = _isFiltering.asStateFlow()

    private var latestResults: List<Transaction> = emptyList()

    init {
        loadTransactions()
        // Live search: debounce keystrokes and re-query the ledger.
        viewModelScope.launch {
            _searchQuery
                .debounce(300)
                .distinctUntilChanged()
                .collect { search() }
        }
    }

    fun loadTransactions() {
        viewModelScope.launch {
            _uiState.value = TransactionsUiState.Loading
            try {
                val transactions = transferApi.getTransactions(accountId)
                latestResults = transactions
                if (transactions.isNotEmpty()) {
                    _uiState.value = TransactionsUiState.Success(transactions)
                } else {
                    _uiState.value = TransactionsUiState.Empty
                }
            } catch (e: Exception) {
                _uiState.value = TransactionsUiState.Error(e.message ?: "Network error occurred")
            }
        }
    }

    fun onSearchChange(query: String) {
        _searchQuery.value = query
    }

    fun onCategorySelected(category: String?) {
        _selectedCategory.value = category
        search()
    }

    fun onTypeSelected(type: String?) {
        _selectedType.value = type
        search()
    }

    fun clearFilters() {
        _searchQuery.value = ""
        _selectedCategory.value = null
        _selectedType.value = null
        loadTransactions()
    }

    private fun search() {
        val query = _searchQuery.value.trim()
        val category = _selectedCategory.value
        val type = _selectedType.value
        if (query.isEmpty() && category == null && type == null) {
            if (latestResults.isNotEmpty()) {
                _uiState.value = TransactionsUiState.Success(latestResults)
            }
            return
        }

        viewModelScope.launch {
            _isFiltering.value = true
            try {
                val results = transferApi.searchTransactions(
                    accountId = accountId,
                    query = query,
                    category = category,
                    type = type
                )
                if (results.isNotEmpty()) {
                    _uiState.value = TransactionsUiState.Success(results)
                } else {
                    _uiState.value = TransactionsUiState.Empty
                }
            } catch (e: Exception) {
                // Keep the previous list on transient search failures.
                if (latestResults.isNotEmpty()) {
                    _uiState.value = TransactionsUiState.Success(latestResults)
                } else {
                    _uiState.value = TransactionsUiState.Error(e.message ?: "Search failed")
                }
            } finally {
                _isFiltering.value = false
            }
        }
    }
}
