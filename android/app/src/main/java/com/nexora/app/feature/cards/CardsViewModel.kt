package com.nexora.app.feature.cards

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.nexora.app.core.model.Card
import com.nexora.app.core.network.api.CardApi
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

@HiltViewModel
class CardsViewModel @Inject constructor(
    private val cardApi: CardApi
) : ViewModel() {

    private val _uiState = MutableStateFlow<CardsUiState>(CardsUiState.Loading)
    val uiState: StateFlow<CardsUiState> = _uiState.asStateFlow()

    init {
        loadCards()
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

    fun freezeCard(cardId: String) {
        viewModelScope.launch {
            try {
                cardApi.freezeCard(cardId)
                loadCards()
            } catch (e: Exception) {
                _uiState.value = CardsUiState.Error(e.message ?: "Failed to freeze card")
            }
        }
    }

    fun unfreezeCard(cardId: String) {
        viewModelScope.launch {
            try {
                cardApi.unfreezeCard(cardId)
                loadCards()
            } catch (e: Exception) {
                _uiState.value = CardsUiState.Error(e.message ?: "Failed to unfreeze card")
            }
        }
    }
}
