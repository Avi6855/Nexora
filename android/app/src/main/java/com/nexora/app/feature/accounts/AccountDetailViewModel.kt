package com.nexora.app.feature.accounts

import androidx.lifecycle.SavedStateHandle
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

sealed class AccountDetailUiState {
    data object Loading : AccountDetailUiState()
    data class Success(val account: Account) : AccountDetailUiState()
    data class Error(val message: String) : AccountDetailUiState()
}

@HiltViewModel
class AccountDetailViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val accountApi: AccountApi
) : ViewModel() {

    private val accountId: String = savedStateHandle["accountId"] ?: ""

    private val _uiState = MutableStateFlow<AccountDetailUiState>(AccountDetailUiState.Loading)
    val uiState: StateFlow<AccountDetailUiState> = _uiState.asStateFlow()

    init {
        loadAccount()
    }

    fun loadAccount() {
        viewModelScope.launch {
            _uiState.value = AccountDetailUiState.Loading
            try {
                val response = accountApi.getAccount(accountId)
                if (response.isSuccess && response.data != null) {
                    _uiState.value = AccountDetailUiState.Success(response.data)
                } else {
                    _uiState.value = AccountDetailUiState.Error(response.error ?: "Failed to load account")
                }
            } catch (e: Exception) {
                _uiState.value = AccountDetailUiState.Error(e.message ?: "Network error occurred")
            }
        }
    }
}
