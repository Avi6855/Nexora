package com.nexora.app.feature.pots

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.nexora.app.core.model.Pot
import com.nexora.app.core.network.api.PotApi
import com.nexora.app.core.network.api.CreatePotRequest
import com.nexora.app.core.network.api.DepositRequest
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed class PotsUiState {
    data object Loading : PotsUiState()
    data class Success(val pots: List<Pot>) : PotsUiState()
    data object Empty : PotsUiState()
    data class Error(val message: String) : PotsUiState()
}

@HiltViewModel
class PotsViewModel @Inject constructor(
    private val potApi: PotApi
) : ViewModel() {

    private val _uiState = MutableStateFlow<PotsUiState>(PotsUiState.Loading)
    val uiState: StateFlow<PotsUiState> = _uiState.asStateFlow()

    init {
        loadPots()
    }

    fun loadPots() {
        viewModelScope.launch {
            _uiState.value = PotsUiState.Loading
            try {
                val response = potApi.getPots()
                if (response.isSuccess && !response.data.isNullOrEmpty()) {
                    _uiState.value = PotsUiState.Success(response.data)
                } else if (response.data.isNullOrEmpty()) {
                    _uiState.value = PotsUiState.Empty
                } else {
                    _uiState.value = PotsUiState.Error(response.error ?: "Failed to load pots")
                }
            } catch (e: Exception) {
                _uiState.value = PotsUiState.Error(e.message ?: "Network error occurred")
            }
        }
    }

    fun depositToPot(potId: String, amount: Long) {
        viewModelScope.launch {
            try {
                potApi.depositToPot(potId, DepositRequest(amount))
                loadPots()
            } catch (e: Exception) {
                _uiState.value = PotsUiState.Error(e.message ?: "Failed to deposit")
            }
        }
    }
}
