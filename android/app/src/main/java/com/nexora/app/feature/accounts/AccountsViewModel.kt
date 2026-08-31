package com.nexora.app.feature.accounts

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.nexora.app.core.model.Account
import com.nexora.app.core.network.api.AccountApi
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed class AccountsUiState {
    data object Loading : AccountsUiState()
    data class Success(val accounts: List<Account>) : AccountsUiState()
    data object Empty : AccountsUiState()
    data class Error(val message: String) : AccountsUiState()
}

@HiltViewModel
class AccountsViewModel @Inject constructor(
    private val accountApi: AccountApi
) : ViewModel() {

    private val _uiState = MutableStateFlow<AccountsUiState>(AccountsUiState.Loading)
    val uiState: StateFlow<AccountsUiState> = _uiState.asStateFlow()

    init {
        loadAccounts()
    }

    fun loadAccounts() {
        viewModelScope.launch {
            _uiState.value = AccountsUiState.Loading
            try {
                val accounts = accountApi.getAccounts()
                if (accounts.isNotEmpty()) {
                    _uiState.value = AccountsUiState.Success(accounts)
                } else {
                    _uiState.value = AccountsUiState.Empty
                }
            } catch (e: Exception) {
                _uiState.value = AccountsUiState.Error(e.message ?: "Network error occurred")
            }
        }
    }
}
