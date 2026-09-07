package com.nexora.app.feature.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.nexora.app.core.model.Account
import com.nexora.app.core.model.RunwayResult
import com.nexora.app.core.model.Transaction
import com.nexora.app.core.network.api.AccountApi
import com.nexora.app.core.network.api.InsightsApi
import com.nexora.app.core.network.api.TransferApi
import com.nexora.app.core.realtime.RealtimeEvent
import com.nexora.app.core.realtime.RealtimeRepository
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
        val recentTransactions: List<Transaction>,
        val runway: RunwayResult? = null
    ) : HomeUiState()
    data object Empty : HomeUiState()
    data class Error(val message: String) : HomeUiState()
}

@HiltViewModel
class HomeViewModel @Inject constructor(
    private val accountApi: AccountApi,
    private val transferApi: TransferApi,
    private val insightsApi: InsightsApi,
    private val realtime: RealtimeRepository
) : ViewModel() {

    private val _uiState = MutableStateFlow<HomeUiState>(HomeUiState.Loading)
    val uiState: StateFlow<HomeUiState> = _uiState.asStateFlow()

    /** Latest live event from the SSE stream, for the "money just moved" banner. */
    private val _liveEvent = MutableStateFlow<RealtimeEvent?>(null)
    val liveEvent: StateFlow<RealtimeEvent?> = _liveEvent.asStateFlow()

    init {
        loadData()
        observeRealtime()
        realtime.connect()
    }

    private fun observeRealtime() {
        viewModelScope.launch {
            realtime.events.collect { event ->
                _liveEvent.value = event
                // Money moved: refresh balances + the recent list in place
                // (no full-screen spinner — the Monzo way).
                refreshInBackground()
            }
        }
    }

    private fun refreshInBackground() {
        viewModelScope.launch {
            try {
                val accounts = accountApi.getAccounts()
                if (accounts.isEmpty()) return@launch
                val transactions = mutableListOf<Transaction>()
                for (account in accounts) {
                    try {
                        transactions.addAll(transferApi.getTransactions(account.id, limit = 5))
                    } catch (_: Exception) {
                    }
                }
                val current = _uiState.value
                if (current is HomeUiState.Success) {
                    _uiState.value = current.copy(
                        accounts = accounts,
                        totalBalance = accounts.sumOf { it.balance },
                        availableBalance = accounts.sumOf { it.availableBalance },
                        pendingBalance = accounts.sumOf { it.pendingBalance },
                        reservedBalance = accounts.sumOf { it.reservedBalance },
                        recentTransactions = transactions
                            .sortedByDescending { it.createdAt }
                            .take(10)
                    )
                } else {
                    loadData()
                }
            } catch (_: Exception) {
                // Transient network failure: the SSE stream stays up and will
                // trigger another refresh on the next event.
            }
        }
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
                        recentTransactions = recentTransactions,
                        runway = fetchRunway(accounts)
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

    /**
     * Financial resilience runway for the first account (best-effort: null
     * when the intelligence engine has no history yet).
     */
    private suspend fun fetchRunway(accounts: List<Account>): RunwayResult? = try {
        insightsApi.getRunway(accounts.first().id)
    } catch (_: Exception) {
        null
    }

    fun consumeLiveEvent() {
        _liveEvent.value = null
    }

    override fun onCleared() {
        realtime.disconnect()
        super.onCleared()
    }
}
