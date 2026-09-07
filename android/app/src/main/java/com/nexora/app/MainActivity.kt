package com.nexora.app

import android.os.Bundle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.fragment.app.FragmentActivity
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.nexora.app.core.datastore.PreferencesDataStore
import com.nexora.app.core.design.theme.NexoraTheme
import com.nexora.app.core.security.BiometricGate
import com.nexora.app.core.security.BiometricResult
import com.nexora.app.navigation.NexoraNavGraph
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.Flow
import javax.inject.Inject

/**
 * FragmentActivity (not plain ComponentActivity) so the system BiometricPrompt
 * can be shown for the app lock. When the user has enabled biometrics in
 * Settings, nothing is revealed until they unlock — Monzo-style.
 */
@AndroidEntryPoint
class MainActivity : FragmentActivity() {

    @Inject lateinit var biometricGate: BiometricGate

    @Inject lateinit var preferencesDataStore: PreferencesDataStore

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            NexoraTheme {
                Surface(
                    modifier = Modifier.fillMaxSize(),
                    color = MaterialTheme.colorScheme.background
                ) {
                    AppLockGate(
                        activity = this,
                        gate = biometricGate,
                        biometricEnabled = preferencesDataStore.biometricEnabled,
                        content = { NexoraNavGraph() }
                    )
                }
            }
        }
    }
}

@Composable
private fun AppLockGate(
    activity: FragmentActivity,
    gate: BiometricGate,
    biometricEnabled: Flow<Boolean>,
    content: @Composable () -> Unit
) {
    var locked by remember { mutableStateOf(false) }
    var checked by remember { mutableStateOf(false) }
    val enabled by biometricEnabled.collectAsStateWithLifecycle(initialValue = false)

    // Decide the initial lock state once (locked = has a stored session).
    LaunchedEffect(Unit) {
        locked = gate.shouldLock()
        checked = true
    }

    // Show the prompt whenever we are locked and the user opted in.
    LaunchedEffect(locked, enabled, checked) {
        if (checked && locked && enabled) {
            gate.authenticate(activity) { result ->
                when (result) {
                    is BiometricResult.Success -> locked = false
                    // Stay locked on cancel/failure; the user can retry.
                    else -> locked = true
                }
            }
        }
    }

    if (checked && locked && enabled) {
        LockScreen(onRetry = {
            gate.authenticate(activity) { result ->
                if (result is BiometricResult.Success) locked = false
            }
        })
    } else {
        content()
    }
}

@Composable
private fun LockScreen(onRetry: () -> Unit) {
    Column(
        modifier = Modifier.fillMaxSize(),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center
    ) {
        Text(
            text = "Nexora is locked",
            style = MaterialTheme.typography.headlineMedium,
            fontWeight = FontWeight.Bold
        )
        Spacer(modifier = Modifier.height(8.dp))
        Text(
            text = "Unlock with your fingerprint or face to continue",
            style = MaterialTheme.typography.bodyLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant
        )
        Spacer(modifier = Modifier.height(24.dp))
        Button(onClick = onRetry) {
            Text("Unlock")
        }
    }
}
