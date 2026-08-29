# ADR-019: Android Architecture

## Status

Accepted

## Context

Nexora's Android application needs a maintainable, testable architecture that supports rapid feature development, parallel team work, and high code quality. The app handles sensitive financial operations (payments, transfers, account management) where correctness and security are paramount.

## Decision

We will use Clean Architecture with feature modules, MVVM presentation pattern, and Jetpack Compose for UI.

## Alternatives

### MVI (Model-View-Intent)
- **Pros**: Unidirectional data flow, predictable state management
- **Cons**: More boilerplate, steeper learning curve, less community adoption

### Single-Module Architecture
- **Pros**: Simple project structure, fast builds initially
- **Cons**: Slow incremental builds, unclear boundaries, hard to enforce architecture

### XML Views (Traditional Android)
- **Pros**: Mature tooling, wide documentation, familiar to most Android developers
- **Cons**: Verbose, harder to test, no declarative UI benefits

## Trade-offs

### Gained
- Clear separation of concerns (data, domain, presentation)
- Feature modules enable parallel development
- MVVM with Compose reduces boilerplate and improves testability
- Domain layer is framework-agnostic and testable
- Each feature can be developed, tested, and reviewed independently

### Lost
- Simpler project structure
- Lower initial learning curve
- XML layout familiarity

## Consequences

### Positive
- Business logic in domain layer is testable without Android framework
- Feature modules reduce build times incrementally
- Compose UI is declarative and easier to test
- Clear boundaries prevent architecture violations
- New developers can understand one feature module at a time

### Negative
- More files per feature (data, domain, presentation layers)
- Module coordination requires clear dependency management
- Compose adoption requires team training

## Implementation Notes

### Module Structure
```
android/
├── app/                          # Application shell
├── core/
│   ├── data/                     # Network, database, repositories
│   ├── domain/                    # Use cases, entities
│   ├── common/                    # Shared utilities
│   └── security/                  # Auth, encryption
├── features/
│   ├── payments/                  # Payment feature module
│   │   ├── data/
│   │   ├── domain/
│   │   └── presentation/
│   ├── accounts/                  # Account management
│   ├── transfers/                 # Money transfers
│   ├── cards/                     # Card management
│   └── settings/                  # User settings
└── build.gradle.kts
```

### Domain Layer (Framework-Agnostic)
```kotlin
// Entity
data class Payment(
    val id: UUID,
    val amount: Money,
    val status: PaymentStatus,
    val createdAt: Instant
)

// Use Case
class InitiatePaymentUseCase(
    private val paymentRepository: PaymentRepository,
    private val policyEngine: PolicyEngine
) {
    suspend operator fun invoke(request: PaymentRequest): Result<Payment> {
        val validation = policyEngine.validate(request)
        if (validation.isFailure) return Result.failure(validation.exceptionOrNull()!!)
        return paymentRepository.create(request)
    }
}

// Repository Interface
interface PaymentRepository {
    suspend fun create(request: PaymentRequest): Result<Payment>
    suspend fun getById(id: UUID): Result<Payment>
    suspend fun getByStatus(status: PaymentStatus): Result<List<Payment>>
}
```

### Presentation Layer (MVVM + Compose)
```kotlin
// ViewModel
@HiltViewModel
class PaymentViewModel @Inject constructor(
    private val initiatePayment: InitiatePaymentUseCase
) : ViewModel() {
    private val _state = MutableStateFlow(PaymentUiState())
    val state: StateFlow<PaymentUiState> = _state.asStateFlow()

    fun initiatePayment(request: PaymentRequest) {
        viewModelScope.launch {
            _state.update { it.copy(isLoading = true) }
            initiatePayment(request)
                .onSuccess { payment -> _state.update { it.copy(payment = payment) } }
                .onFailure { error -> _state.update { it.copy(error = error.message) } }
        }
    }
}

// Compose Screen
@Composable
fun PaymentScreen(viewModel: PaymentViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    // Declarative UI...
}
```
