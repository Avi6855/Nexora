package com.nexora.app.feature.security

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.nexora.app.core.datastore.PreferencesDataStore
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

@HiltViewModel
class SecurityViewModel @Inject constructor(
    private val preferencesDataStore: PreferencesDataStore
) : ViewModel() {

    private val _biometricEnabled = MutableStateFlow(false)
    val biometricEnabled: StateFlow<Boolean> = _biometricEnabled.asStateFlow()

    init {
        viewModelScope.launch {
            preferencesDataStore.biometricEnabled.collect {
                _biometricEnabled.value = it
            }
        }
    }

    fun toggleBiometric() {
        viewModelScope.launch {
            preferencesDataStore.setBiometricEnabled(!_biometricEnabled.value)
        }
    }
}
