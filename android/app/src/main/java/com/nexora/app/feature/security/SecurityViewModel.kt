package com.nexora.app.feature.security

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.nexora.app.core.datastore.PreferencesDataStore
import com.nexora.app.core.network.api.AccountApi
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

@HiltViewModel
class SecurityViewModel @Inject constructor(
    private val preferencesDataStore: PreferencesDataStore,
    private val accountApi: AccountApi
) : ViewModel() {

    private val _biometricEnabled = MutableStateFlow(false)
    val biometricEnabled: StateFlow<Boolean> = _biometricEnabled.asStateFlow()

    /** Emergency lockdown state across the user's accounts (any locked = on). */
    private val _lockdownEnabled = MutableStateFlow(false)
    val lockdownEnabled: StateFlow<Boolean> = _lockdownEnabled.asStateFlow()

    private val _lockdownBusy = MutableStateFlow(false)
    val lockdownBusy: StateFlow<Boolean> = _lockdownBusy.asStateFlow()

    private val _lockdownError = MutableStateFlow<String?>(null)
    val lockdownError: StateFlow<String?> = _lockdownError.asStateFlow()

    private var accountIds: List<String> = emptyList()

    init {
        viewModelScope.launch {
            preferencesDataStore.biometricEnabled.collect {
                _biometricEnabled.value = it
            }
        }
        refreshLockdown()
    }

    fun toggleBiometric() {
        viewModelScope.launch {
            preferencesDataStore.setBiometricEnabled(!_biometricEnabled.value)
        }
    }

    /** Reads the accounts and mirrors whether any is in lockdown. */
    fun refreshLockdown() {
        viewModelScope.launch {
            try {
                val accounts = accountApi.getAccounts()
                accountIds = accounts.map { it.id }
                _lockdownEnabled.value = accounts.any { it.lockdownEnabled }
            } catch (_: Exception) {
                // Lockdown state simply stays as-is on network failure.
            }
        }
    }

    /**
     * Toggles emergency lockdown on ALL of the user's accounts: stolen-device
     * scenario means "lock everything now", so per-account granularity would
     * be the wrong default here.
     */
    fun toggleLockdown() {
        val target = !_lockdownEnabled.value
        viewModelScope.launch {
            _lockdownBusy.value = true
            _lockdownError.value = null
            try {
                if (accountIds.isEmpty()) {
                    accountIds = accountApi.getAccounts().map { it.id }
                }
                var anyFailure: String? = null
                for (id in accountIds) {
                    try {
                        accountApi.setLockdown(id, com.nexora.app.core.network.api.LockdownRequest(target))
                    } catch (e: Exception) {
                        anyFailure = e.message ?: "Lockdown request failed"
                    }
                }
                if (anyFailure != null) {
                    _lockdownError.value = anyFailure
                } else {
                    _lockdownEnabled.value = target
                }
            } catch (e: Exception) {
                _lockdownError.value = e.message ?: "Network error occurred"
            } finally {
                _lockdownBusy.value = false
            }
        }
    }

    fun consumeLockdownError() {
        _lockdownError.value = null
    }
}
