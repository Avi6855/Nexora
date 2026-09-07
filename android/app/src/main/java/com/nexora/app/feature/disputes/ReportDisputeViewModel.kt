package com.nexora.app.feature.disputes

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.nexora.app.core.model.CreateDisputeRequest
import com.nexora.app.core.model.DisputeCase
import com.nexora.app.core.network.api.DisputeApi
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed class ReportDisputeUiState {
    data object Editing : ReportDisputeUiState()
    data object Submitting : ReportDisputeUiState()
    data class Done(val case: DisputeCase) : ReportDisputeUiState()
    data class Error(val message: String) : ReportDisputeUiState()
}

/**
 * ReportDisputeViewModel submits a dispute for a ledger entry and surfaces
 * the eligibility verdict immediately (the backend runs the scheme-rules
 * engine at creation).
 */
@HiltViewModel
class ReportDisputeViewModel @Inject constructor(
    private val disputeApi: DisputeApi
) : ViewModel() {

    private val _uiState = MutableStateFlow<ReportDisputeUiState>(ReportDisputeUiState.Editing)
    val uiState: StateFlow<ReportDisputeUiState> = _uiState.asStateFlow()

    fun submit(entryId: String, reason: String, description: String) {
        viewModelScope.launch {
            _uiState.value = ReportDisputeUiState.Submitting
            try {
                val case = disputeApi.createDispute(
                    CreateDisputeRequest(
                        entryId = entryId,
                        reason = reason,
                        description = description
                    )
                )
                _uiState.value = ReportDisputeUiState.Done(case)
            } catch (e: Exception) {
                _uiState.value = ReportDisputeUiState.Error(
                    e.message ?: "Could not raise the dispute"
                )
            }
        }
    }
}
