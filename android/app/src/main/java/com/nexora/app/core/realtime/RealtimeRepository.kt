package com.nexora.app.core.realtime

import com.google.gson.Gson
import com.google.gson.JsonObject
import com.google.gson.JsonPrimitive
import com.nexora.app.core.security.SecureTokenStorage
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.sse.EventSource
import okhttp3.sse.EventSourceListener
import okhttp3.sse.EventSources
import java.util.concurrent.TimeUnit
import javax.inject.Inject
import javax.inject.Singleton

/**
 * A live money event pushed by the notification service over SSE
 * (GET /v1/stream). Mirrors the feed-item payload the service persists and
 * fans out (see notification-service realtime consumer + feed).
 */
data class RealtimeEvent(
    val type: String,          // e.g. "card.authorization.approved"
    val title: String = "",
    val body: String = "",
    val accountId: String? = null,
    val amount: Long? = null,
    val currency: String? = null,
    val merchant: String? = null,
    val balanceAfter: Long? = null
) {
    val isMoneyEvent: Boolean
        get() = amount != null
}

sealed class ConnectionState {
    data object Disconnected : ConnectionState()
    data object Connecting : ConnectionState()
    data object Connected : ConnectionState()
}

/**
 * RealtimeRepository holds the single SSE connection for the app. Every
 * screen that shows money (Home, Accounts, Transactions) observes [events]
 * and refreshes when something moves — the Monzo-style live feed, backed by
 * the same durable notifications the backend persists.
 *
 * The notification service auths the stream via the standard bearer token,
 * so no separate handshake is needed. Reconnects with exponential backoff.
 */
@Singleton
class RealtimeRepository @Inject constructor(
    private val tokenStorage: SecureTokenStorage
) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    private val _events = MutableSharedFlow<RealtimeEvent>(extraBufferCapacity = 32)
    val events: SharedFlow<RealtimeEvent> = _events.asSharedFlow()

    private val _connectionState = MutableStateFlow<ConnectionState>(ConnectionState.Disconnected)
    val connectionState: StateFlow<ConnectionState> = _connectionState.asStateFlow()

    private val client: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(0, TimeUnit.MILLISECONDS) // SSE is a long-lived stream
        .build()

    @Volatile
    private var eventSource: EventSource? = null

    @Volatile
    private var shouldRun = false

    /** Signals the run loop that the current connection has terminated. */
    @Volatile
    private var connectionDone: CompletableDeferred<Unit> = CompletableDeferred()

    /**
     * Starts (or restarts) the stream for the current user. Safe to call
     * repeatedly; reconnects with exponential backoff while [shouldRun].
     */
    fun connect() {
        if (shouldRun) return
        shouldRun = true
        scope.launch { runLoop() }
    }

    fun disconnect() {
        shouldRun = false
        eventSource?.cancel()
        eventSource = null
        connectionDone.complete(Unit)
        _connectionState.value = ConnectionState.Disconnected
    }

    private suspend fun runLoop() {
        var attempt = 0
        while (shouldRun) {
            val token = tokenStorage.getAccessToken()
            if (token.isNullOrBlank()) {
                _connectionState.value = ConnectionState.Disconnected
                delay(5_000)
                continue
            }

            val request = Request.Builder()
                .url("$BASE_URL/v1/stream")
                .header("Authorization", "Bearer $token")
                .header("Accept", "text/event-stream")
                .build()

            connectionDone = CompletableDeferred()
            _connectionState.value = ConnectionState.Connecting
            eventSource = EventSources.createFactory(client).newEventSource(request, listener)

            // Suspend until the connection closes/fails, then back off.
            connectionDone.await()
            if (!shouldRun) break

            attempt = (attempt + 1).coerceAtMost(5)
            delay((1L shl attempt) * 1_000) // 2s, 4s, 8s ... capped at 32s
        }
    }

    private val listener = object : EventSourceListener() {
        override fun onOpen(eventSource: EventSource, response: Response) {
            _connectionState.value = ConnectionState.Connected
        }

        override fun onClosed(eventSource: EventSource) {
            _connectionState.value = ConnectionState.Disconnected
            connectionDone.complete(Unit)
        }

        override fun onFailure(eventSource: EventSource, t: Throwable?, response: Response?) {
            _connectionState.value = ConnectionState.Disconnected
            connectionDone.complete(Unit)
        }

        override fun onEvent(eventSource: EventSource, id: String?, type: String?, data: String) {
            when (type) {
                "ready" -> _connectionState.value = ConnectionState.Connected
                "feed_item" -> parseAndEmit(data)
            }
        }
    }

    private fun parseAndEmit(data: String) {
        try {
            val obj = gson.fromJson(data, JsonObject::class.java) ?: return
            val payload = obj.getAsJsonObject("payload") ?: obj
            val type = obj.str("event_type")
                .takeIf { it.isNotEmpty() }
                ?: payload.str("event_type").takeIf { it.isNotEmpty() }
                ?: "feed_item"

            _events.tryEmit(
                RealtimeEvent(
                    type = type,
                    title = payload.str("title"),
                    body = payload.str("body"),
                    accountId = payload.str("account_id").takeIf { it.isNotEmpty() },
                    amount = payload.num("amount"),
                    currency = payload.str("currency").takeIf { it.isNotEmpty() },
                    merchant = payload.str("merchant").takeIf { it.isNotEmpty() },
                    balanceAfter = payload.num("balance_after")
                )
            )
        } catch (_: Exception) {
            // Malformed payload: ignore, the stream stays healthy.
        }
    }

    private fun JsonObject.str(name: String): String {
        val v = get(name) as? JsonPrimitive ?: return ""
        return if (v.isString) v.asString else ""
    }

    private fun JsonObject.num(name: String): Long? {
        val v = get(name) as? JsonPrimitive ?: return null
        return if (v.isNumber) v.asLong else null
    }

    companion object {
        // Emulator -> host machine gateway (same base URL as Retrofit).
        const val BASE_URL = "http://10.0.2.2:8000"

        private val gson = Gson()
    }
}
