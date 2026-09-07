package com.nexora.app.feature.cards

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.nexora.app.core.model.Card
import com.nexora.app.core.network.api.CardApi
import com.nexora.app.core.network.api.UpdateControlsRequest
import com.nexora.app.core.network.api.UpdateLimitsRequest
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed class CardsUiState {
    data object Loading : CardsUiState()
    data class Success(val cards: List<Card>) : CardsUiState()
    data object Empty : CardsUiState()
    data class Error(val message: String) : CardsUiState()
}

/** State for the single-card detail screen (limits + channel controls). */
sealed class CardDetailUiState {
    data object Loading : CardDetailUiState()
    data class Success(val card: Card) : CardDetailUiState()
    data class Error(val message: String) : CardDetailUiState()
}

@HiltViewModel
class CardsViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val cardApi: CardApi
) : ViewModel() {

    // Populated only on the CardDetail route; empty on the list route.
    private val cardId: String = savedStateHandle["cardId"] ?: ""

    private val _uiState = MutableStateFlow<CardsUiState>(CardsUiState.Loading)
    val uiState: StateFlow<CardsUiState> = _uiState.asStateFlow()

    private val _detailState = MutableStateFlow<CardDetailUiState>(CardDetailUiState.Loading)
    val detailState: StateFlow<CardDetailUiState> = _detailState.asStateFlow()

    private val _actionError = MutableStateFlow<String?>(null)
    val actionError: StateFlow<String?> = _actionError.asStateFlow()

    init {
        if (cardId.isNotBlank()) {
            loadCardDetail()
        } else {
            loadCards()
        }
    }

    fun loadCards() {
        viewModelScope.launch {
            _uiState.value = CardsUiState.Loading
            try {
                val cards = cardApi.getCards()
                if (cards.isNotEmpty()) {
                    _uiState.value = CardsUiState.Success(cards)
                } else {
                    _uiState.value = CardsUiState.Empty
                }
            } catch (e: Exception) {
                _uiState.value = CardsUiState.Error(e.message ?: "Network error occurred")
            }
        }
    }

    fun loadCardDetail() {
        if (cardId.isBlank()) return
        viewModelScope.launch {
            _detailState.value = CardDetailUiState.Loading
            try {
                _detailState.value = CardDetailUiState.Success(cardApi.getCard(cardId))
            } catch (e: Exception) {
                _detailState.value = CardDetailUiState.Error(e.message ?: "Failed to load card")
            }
        }
    }

    fun freezeCard(cardId: String) {
        viewModelScope.launch {
            try {
                cardApi.freezeCard(cardId)
                loadCards()
                if (cardId == this@CardsViewModel.cardId) loadCardDetail()
            } catch (e: Exception) {
                _actionError.value = e.message ?: "Failed to freeze card"
            }
        }
    }

    fun unfreezeCard(cardId: String) {
        viewModelScope.launch {
            try {
                cardApi.unfreezeCard(cardId)
                loadCards()
                if (cardId == this@CardsViewModel.cardId) loadCardDetail()
            } catch (e: Exception) {
                _actionError.value = e.message ?: "Failed to unfreeze card"
            }
        }
    }

    /** Persists new daily/monthly limits and refreshes whatever is showing. */
    fun updateLimits(dailyLimit: Long, monthlyLimit: Long) {
        if (cardId.isBlank()) return
        viewModelScope.launch {
            try {
                cardApi.updateLimits(cardId, UpdateLimitsRequest(dailyLimit, monthlyLimit))
                loadCardDetail()
            } catch (e: Exception) {
                _actionError.value = e.message ?: "Failed to update limits"
            }
        }
    }

    /** Persists channel toggles; only the provided flags are sent. */
    fun updateControls(online: Boolean? = null, atm: Boolean? = null, gamblingBlock: Boolean? = null) {
        if (cardId.isBlank()) return
        viewModelScope.launch {
            try {
                cardApi.updateControls(cardId, UpdateControlsRequest(online, atm, gamblingBlock))
                loadCardDetail()
            } catch (e: Exception) {
                _actionError.value = e.message ?: "Failed to update card controls"
            }
        }
    }

    fun clearActionError() {
        _actionError.value = null
    }
}
