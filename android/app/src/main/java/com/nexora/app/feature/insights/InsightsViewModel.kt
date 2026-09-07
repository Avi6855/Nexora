package com.nexora.app.feature.insights

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.nexora.app.core.model.Account
import com.nexora.app.core.model.InsightAlert
import com.nexora.app.core.model.SafeToSpend
import com.nexora.app.core.model.Subscription
import com.nexora.app.core.network.api.AccountApi
import com.nexora.app.core.network.api.InsightsApi
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

data class AccountSafeToSpend(
    val account: Account,
    val safeToSpend: SafeToSpend
)

sealed class InsightsUiState {
    data object Loading : InsightsUiState()
    data class Success(
        val safeToSpendByAccount: List<AccountSafeToSpend>,
        val subscriptions: List<Subscription>,
        val alerts: List<InsightAlert>,
        val monthlySubscriptionCost: Long
    ) : InsightsUiState()
    data object Empty : InsightsUiState()
    data class Error(val message: String) : InsightsUiState()
}

/**
 * InsightsViewModel drives the Financial Intelligence Platform screen:
 * Safe-to-Spend per account, detected subscriptions (with price-hike flags)
 * and the recent intelligence alerts.
 */
@HiltViewModel
class InsightsViewModel @Inject constructor(
    private val accountApi: AccountApi,
    private val insightsApi: InsightsApi
) : ViewModel() {

    private val _uiState = MutableStateFlow<InsightsUiState>(InsightsUiState.Loading)
    val uiState: StateFlow<InsightsUiState> = _uiState.asStateFlow()

    init {
        loadData()
    }

    fun loadData() {
        viewModelScope.launch {
            _uiState.value = InsightsUiState.Loading
            try {
                val accounts = accountApi.getAccounts()
                if (accounts.isEmpty()) {
                    _uiState.value = InsightsUiState.Empty
                    return@launch
                }

                val perAccount = mutableListOf<AccountSafeToSpend>()
                val subscriptions = mutableListOf<Subscription>()
                val alerts = mutableListOf<InsightAlert>()

                // The demo platform keys insights on account_id; fetch per
                // owned account and merge.
                for (account in accounts) {
                    try {
                        perAccount.add(AccountSafeToSpend(account, insightsApi.getSafeToSpend(account.id)))
                    } catch (_: Exception) {
                        // Safe-to-spend needs baseline history; skip accounts
                        // the engine has no projections for yet.
                    }
                    try {
                        subscriptions.addAll(insightsApi.getSubscriptions(account.id))
                    } catch (_: Exception) {
                    }
                    try {
                        alerts.addAll(insightsApi.getAlerts(account.id, limit = 20))
                    } catch (_: Exception) {
                    }
                }

                _uiState.value = InsightsUiState.Success(
                    safeToSpendByAccount = perAccount,
                    subscriptions = subscriptions.distinctBy { it.subscriptionId },
                    alerts = alerts.sortedByDescending { it.createdAt },
                    monthlySubscriptionCost = subscriptions.sumOf { it.monthlyAmount }
                )
            } catch (e: Exception) {
                _uiState.value = InsightsUiState.Error(e.message ?: "Network error occurred")
            }
        }
    }
}
