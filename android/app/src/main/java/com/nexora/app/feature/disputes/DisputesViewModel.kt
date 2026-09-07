package com.nexora.app.feature.disputes

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.nexora.app.core.model.DisputeCase
import com.nexora.app.core.network.api.DisputeApi
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed class DisputesUiState {
    data object Loading : DisputesUiState()
    data class Success(
        val openCases: List<DisputeCase>,
        val resolvedCases: List<DisputeCase>
    ) : DisputesUiState()
    data object Empty : DisputesUiState()
    data class Error(val message: String) : DisputesUiState()
}

/**
 * DisputesViewModel drives the Dispute Orchestration list: open cases with
 * their current workflow stage + deadlines, and resolved history.
 */
@HiltViewModel
class DisputesViewModel @Inject constructor(
    private val disputeApi: DisputeApi
) : ViewModel() {

    private val _uiState = MutableStateFlow<DisputesUiState>(DisputesUiState.Loading)
    val uiState: StateFlow<DisputesUiState> = _uiState.asStateFlow()

    init {
        loadData()
    }

    fun loadData() {
        viewModelScope.launch {
            _uiState.value = DisputesUiState.Loading
            try {
                val cases = disputeApi.listDisputes()
                if (cases.isEmpty()) {
                    _uiState.value = DisputesUiState.Empty
                } else {
                    _uiState.value = DisputesUiState.Success(
                        openCases = cases.filter { it.isOpen },
                        resolvedCases = cases.filterNot { it.isOpen }
                    )
                }
            } catch (e: Exception) {
                _uiState.value = DisputesUiState.Error(e.message ?: "Network error occurred")
            }
        }
    }
}
