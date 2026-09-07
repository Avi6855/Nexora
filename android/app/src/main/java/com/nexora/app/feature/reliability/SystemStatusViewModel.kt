package com.nexora.app.feature.reliability

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.nexora.app.core.model.DependencyGraph
import com.nexora.app.core.network.api.ReliabilityApi
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed class SystemStatusUiState {
    data object Loading : SystemStatusUiState()
    data class Success(
        val graph: DependencyGraph,
        val unhealthyCount: Int
    ) : SystemStatusUiState()
    data class Error(val message: String) : SystemStatusUiState()
}

/**
 * SystemStatusViewModel polls the control-plane dependency health graph: the
 * live platform view (which services are healthy, what depends on what, and
 * the auto-throttle advisories).
 */
@HiltViewModel
class SystemStatusViewModel @Inject constructor(
    private val reliabilityApi: ReliabilityApi
) : ViewModel() {

    private val _uiState = MutableStateFlow<SystemStatusUiState>(SystemStatusUiState.Loading)
    val uiState: StateFlow<SystemStatusUiState> = _uiState.asStateFlow()

    init {
        load()
    }

    fun load() {
        viewModelScope.launch {
            try {
                val graph = reliabilityApi.getDependencyGraph()
                _uiState.value = SystemStatusUiState.Success(
                    graph = graph,
                    unhealthyCount = graph.nodes.count {
                        it.status != "HEALTHY"
                    }
                )
            } catch (e: Exception) {
                _uiState.value = SystemStatusUiState.Error(
                    e.message ?: "Could not reach the control plane"
                )
            }
        }
    }
}
