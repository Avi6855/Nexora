# ADR-020: Real-Time Payment Status Updates

## Status

Accepted

## Context

Nexora payment states change asynchronously—initiated, processing, completed, failed. Users need timely status updates without refreshing the app. The system must handle varying provider response times (instant to several minutes) and provide a responsive experience without overwhelming backend services with polling requests.

## Decision

We will use polling with exponential backoff for payment status updates, starting at 1 second and capping at 30 seconds, with long-polling support for critical status transitions.

## Alternatives

### WebSocket
- **Pros**: True real-time, bidirectional, low latency
- **Cons**: Connection management complexity, scaling challenges, stateful infrastructure

### Server-Sent Events (SSE)
- **Pros**: Simpler than WebSocket, automatic reconnection, HTTP-based
- **Cons**: Unidirectional, limited browser support on mobile, connection limits

### Push Notifications
- **Pros**: Works when app is backgrounded, no persistent connection
- **Cons**: Delivery not guaranteed, latency, platform dependency

## Trade-offs

### Gained
- Simpler infrastructure (no WebSocket servers)
- Works through load balancers and proxies without special configuration
- Automatic retry on network failures
- Graceful degradation during high load
- Compatible with all network conditions

### Lost
- True real-time updates (sub-second)
- Bidirectional communication
- Server-initiated push capability

## Consequences

### Positive
- Payment status updates within seconds of state change
- No additional infrastructure for WebSocket management
- Exponential backoff reduces server load during high traffic
- Critical transitions (completion, failure) can use long-polling for faster delivery
- Simple to implement and debug

### Negative
- Users may see slight delay compared to WebSocket
- Polling generates additional HTTP requests
- Battery impact on mobile devices from background polling

## Implementation Notes

### Polling Strategy
```kotlin
data class PollingConfig(
    val initialDelayMs: Long = 1000,    // 1 second
    val maxDelayMs: Long = 30000,       // 30 seconds
    val multiplier: Double = 1.5,
    val maxRetries: Int = 20
)

fun pollingStrategy(config: PollingConfig): Flow<Long> = flow {
    var delay = config.initialDelayMs
    repeat(config.maxRetries) { attempt ->
        emit(delay)
        delay = (delay * config.multiplier).toLong().coerceAtMost(config.maxDelayMs)
    }
}
```

### Payment Status Polling
```kotlin
class PaymentStatusPoller @Inject constructor(
    private val paymentApi: PaymentApi
) {
    fun pollPaymentStatus(paymentId: UUID): Flow<PaymentStatus> = flow {
        var currentDelay = 1000L
        val maxDelay = 30000L

        while (true) {
            val status = paymentApi.getPaymentStatus(paymentId)
            emit(status)

            if (status.isTerminal()) break

            delay(currentDelay)
            currentDelay = (currentDelay * 1.5).toLong().coerceAtMost(maxDelay)
        }
    }
}
```

### Long-Polling for Critical Transitions
```kotlin
// For payment completion, use long-polling for faster notification
suspend fun waitForCompletion(paymentId: UUID, timeoutMs: Long = 60000): PaymentStatus {
    return withTimeout(timeoutMs) {
        paymentApi.longPollStatus(paymentId)
    }
}
```

### UI Integration
```kotlin
@Composable
fun PaymentStatusScreen(paymentId: UUID) {
    val poller = remember { PaymentStatusPoller() }
    val status by poller.pollPaymentStatus(paymentId)
        .collectAsState(initial = PaymentStatus.INITIATED)

    when (status) {
        PaymentStatus.PROCESSING -> LoadingIndicator()
        PaymentStatus.COMPLETED -> SuccessScreen()
        PaymentStatus.FAILED -> ErrorScreen()
        else -> StatusIndicator(status)
    }
}
```
