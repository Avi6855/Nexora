package com.nexora.app.core.security

import androidx.appcompat.app.AppCompatActivity
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricPrompt
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Result of a biometric unlock attempt, consumed by the app-lock UI.
 */
sealed class BiometricResult {
    data object Success : BiometricResult()
    data object Cancelled : BiometricResult()
    data class Failed(val message: String) : BiometricResult()
    data class Unavailable(val message: String) : BiometricResult()
}

/**
 * BiometricGate wraps androidx.biometric for the app lock. When the user has
 * enabled "Biometric Authentication" in Settings (biometricEnabled in
 * DataStore), MainActivity shows the lock before any content is revealed,
 * exactly like the real Monzo app.
 */
@Singleton
class BiometricGate @Inject constructor(
    private val tokenStorage: SecureTokenStorage
) {

    /** Whether the lock should appear at all: logged in AND user opted in. */
    fun shouldLock(): Boolean = tokenStorage.isLoggedIn()

    /** Whether the device can actually perform biometric authentication. */
    fun canAuthenticate(activity: FragmentActivity): Boolean {
        val manager = BiometricManager.from(activity)
        return manager.canAuthenticate(ALLOWED_AUTHENTICATORS) == BiometricManager.BIOMETRIC_SUCCESS
    }

    /**
     * Shows the system biometric prompt. [onResult] is invoked exactly once.
     * Falls back to [BiometricResult.Unavailable] when no biometrics are
     * enrolled; callers should offer the device credential in that case.
     */
    fun authenticate(
        activity: FragmentActivity,
        title: String = "Unlock Nexora",
        subtitle: String = "Use your fingerprint or face to unlock your money",
        onResult: (BiometricResult) -> Unit
    ) {
        val manager = BiometricManager.from(activity)
        if (manager.canAuthenticate(ALLOWED_AUTHENTICATORS) != BiometricManager.BIOMETRIC_SUCCESS) {
            onResult(BiometricResult.Unavailable("Biometrics are not available on this device"))
            return
        }

        val prompt = BiometricPrompt(
            activity,
            ContextCompat.getMainExecutor(activity),
            object : BiometricPrompt.AuthenticationCallback() {
                override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) {
                    onResult(BiometricResult.Success)
                }

                override fun onAuthenticationError(errorCode: Int, errString: CharSequence) {
                    when (errorCode) {
                        BiometricPrompt.ERROR_USER_CANCELED,
                        BiometricPrompt.ERROR_NEGATIVE_BUTTON -> onResult(BiometricResult.Cancelled)
                        else -> onResult(BiometricResult.Failed(errString.toString()))
                    }
                }
            }
        )

        prompt.authenticate(
            BiometricPrompt.PromptInfo.Builder()
                .setTitle(title)
                .setSubtitle(subtitle)
                .setAllowedAuthenticators(ALLOWED_AUTHENTICATORS)
                .build()
        )
    }

    companion object {
        private const val ALLOWED_AUTHENTICATORS =
            BiometricManager.Authenticators.BIOMETRIC_WEAK or
            BiometricManager.Authenticators.DEVICE_CREDENTIAL

        /**
         * One-off prompt without a stored session gate (used by the login
         * screen to unlock a saved session with biometrics).
         */
        fun authenticateStatic(activity: FragmentActivity, onResult: (BiometricResult) -> Unit) {
            val manager = BiometricManager.from(activity)
            if (manager.canAuthenticate(ALLOWED_AUTHENTICATORS) != BiometricManager.BIOMETRIC_SUCCESS) {
                onResult(BiometricResult.Unavailable("Biometrics are not available on this device"))
                return
            }
            val prompt = BiometricPrompt(
                activity,
                ContextCompat.getMainExecutor(activity),
                object : BiometricPrompt.AuthenticationCallback() {
                    override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) {
                        onResult(BiometricResult.Success)
                    }

                    override fun onAuthenticationError(errorCode: Int, errString: CharSequence) {
                        when (errorCode) {
                            BiometricPrompt.ERROR_USER_CANCELED,
                            BiometricPrompt.ERROR_NEGATIVE_BUTTON -> onResult(BiometricResult.Cancelled)
                            else -> onResult(BiometricResult.Failed(errString.toString()))
                        }
                    }
                }
            )
            prompt.authenticate(
                BiometricPrompt.PromptInfo.Builder()
                    .setTitle("Unlock Nexora")
                    .setSubtitle("Confirm it's you to continue")
                    .setAllowedAuthenticators(ALLOWED_AUTHENTICATORS)
                    .build()
            )
        }
    }
}
