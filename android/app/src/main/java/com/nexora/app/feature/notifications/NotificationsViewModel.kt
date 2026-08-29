package com.nexora.app.feature.notifications

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.nexora.app.core.model.Notification
import com.nexora.app.core.network.api.NotificationApi
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed class NotificationsUiState {
    data object Loading : NotificationsUiState()
    data class Success(val notifications: List<Notification>) : NotificationsUiState()
    data object Empty : NotificationsUiState()
    data class Error(val message: String) : NotificationsUiState()
}

@HiltViewModel
class NotificationsViewModel @Inject constructor(
    private val notificationApi: NotificationApi
) : ViewModel() {

    private val _uiState = MutableStateFlow<NotificationsUiState>(NotificationsUiState.Loading)
    val uiState: StateFlow<NotificationsUiState> = _uiState.asStateFlow()

    init {
        loadNotifications()
    }

    fun loadNotifications() {
        viewModelScope.launch {
            _uiState.value = NotificationsUiState.Loading
            try {
                val response = notificationApi.getNotifications()
                if (response.isSuccess && !response.data.isNullOrEmpty()) {
                    _uiState.value = NotificationsUiState.Success(response.data)
                } else if (response.data.isNullOrEmpty()) {
                    _uiState.value = NotificationsUiState.Empty
                } else {
                    _uiState.value = NotificationsUiState.Error(response.error ?: "Failed to load notifications")
                }
            } catch (e: Exception) {
                _uiState.value = NotificationsUiState.Error(e.message ?: "Network error occurred")
            }
        }
    }

    fun markAsRead(notificationId: String) {
        viewModelScope.launch {
            try {
                notificationApi.markAsRead(notificationId)
                loadNotifications()
            } catch (e: Exception) {
                _uiState.value = NotificationsUiState.Error(e.message ?: "Failed to mark as read")
            }
        }
    }

    fun markAllAsRead() {
        viewModelScope.launch {
            try {
                notificationApi.markAllAsRead()
                loadNotifications()
            } catch (e: Exception) {
                _uiState.value = NotificationsUiState.Error(e.message ?: "Failed to mark all as read")
            }
        }
    }
}
