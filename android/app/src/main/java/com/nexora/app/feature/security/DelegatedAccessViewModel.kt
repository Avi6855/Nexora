package com.nexora.app.feature.security

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.nexora.app.core.model.CreateGrantRequest
import com.nexora.app.core.model.DelegationGrant
import com.nexora.app.core.network.api.ConsentApi
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

data class DelegatedAccessUiState(
    val loading: Boolean = true,
    val grants: List<DelegationGrant> = emptyList(),
    val error: String? = null
)

/**
 * DelegatedAccessViewModel manages the time-bound, capability-scoped access
 * grants: create (share access), revoke, and list.
 */
@HiltViewModel
class DelegatedAccessViewModel @Inject constructor(
    private val consentApi: ConsentApi
) : ViewModel() {

    private val _uiState = MutableStateFlow(DelegatedAccessUiState())
    val uiState: StateFlow<DelegatedAccessUiState> = _uiState.asStateFlow()

    init {
        loadData()
    }

    fun loadData() {
        viewModelScope.launch {
            _uiState.value = _uiState.value.copy(loading = true, error = null)
            try {
                val grants = consentApi.listGrants()
                _uiState.value = DelegatedAccessUiState(loading = false, grants = grants)
            } catch (e: Exception) {
                _uiState.value = DelegatedAccessUiState(
                    loading = false,
                    error = e.message ?: "Could not load delegated access"
                )
            }
        }
    }

    fun createGrant(email: String, label: String, scopes: Set<String>, durationDays: Int) {
        viewModelScope.launch {
            try {
                consentApi.createGrant(
                    CreateGrantRequest(
                        delegateEmail = email,
                        label = label,
                        scopes = scopes.toList(),
                        durationDays = durationDays
                    )
                )
                loadData()
            } catch (e: Exception) {
                _uiState.value = _uiState.value.copy(
                    loading = false,
                    error = e.message ?: "Could not create the grant"
                )
            }
        }
    }

    fun revokeGrant(grantId: String) {
        viewModelScope.launch {
            try {
                consentApi.revokeGrant(grantId)
                loadData()
            } catch (e: Exception) {
                _uiState.value = _uiState.value.copy(
                    error = e.message ?: "Could not revoke the grant"
                )
            }
        }
    }
}
