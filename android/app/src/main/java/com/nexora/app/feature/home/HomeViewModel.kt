package com.nexora.app.feature.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.nexora.app.core.model.Account
import com.nexora.app.core.model.Transaction
import com.nexora.app.core.network.api.AccountApi
import com.nexora.app.core.network.api.TransferApi
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed class HomeUiState {
    data object Loading : HomeUiState()
    data class Success(
        val accounts: List<Account>,
        val totalBalance: Long,
        val availableBalance: Long,
        val pendingBalance: Long,
        val reservedBalance: Long,
        val recentTransactions: List<Transaction>
    ) : HomeUiState()
    data object Empty : HomeUiState()
    data class Error(val message: String) : HomeUiState()
}

@HiltViewModel
class HomeViewModel @Inject constructor(
    private val accountApi: AccountApi,
    private val transferApi: TransferApi
) : ViewModel() {

    private val _uiState = MutableStateFlow<HomeUiState>(HomeUiState.Loading)
    val uiState: StateFlow<HomeUiState> = _uiState.asStateFlow()

    init {
        loadData()
    }

    fun loadData() {
        viewModelScope.launch {
            _uiState.value = HomeUiState.Loading
            try {
                val accounts = accountApi.getAccounts()
                if (accounts.isNotEmpty()) {
                    val totalBalance = accounts.sumOf { it.balance }
                    val availableBalance = accounts.sumOf { it.availableBalance }
                    val pendingBalance = accounts.sumOf { it.pendingBalance }
                    val reservedBalance = accounts.sumOf { it.reservedBalance }

                    val transactions = mutableListOf<Transaction>()
                    for (account in accounts) {
                        try {
                            val txList = transferApi.getTransactions(account.id, limit = 5)
                            transactions.addAll(txList)
                        } catch (_: Exception) {
                            // ignore transfer failures per-account
                        }
                    }
                    val recentTransactions = transactions
                        .sortedByDescending { it.createdAt }
                        .take(10)

                    _uiState.value = HomeUiState.Success(
                        accounts = accounts,
                        totalBalance = totalBalance,
                        availableBalance = availableBalance,
                        pendingBalance = pendingBalance,
                        reservedBalance = reservedBalance,
                        recentTransactions = recentTransactions
                    )
                } else {
                    _uiState.value = HomeUiState.Empty
                }
            } catch (e: Exception) {
                val msg = e.message ?: "Network error occurred"
                _uiState.value = HomeUiState.Error(msg)
            }
        }
    }
}
