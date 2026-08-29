package provider

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

type PaymentRequest struct {
	PaymentID      string
	AccountID      string
	Amount         int64
	Currency       string
	CounterpartyID string
	Reference      string
}

type PaymentResult struct {
	ProviderID     string
	TransactionRef string
	Status         string
	Message        string
	ResponseCode   string
	ProcessedAt    time.Time
}

type PaymentProvider interface {
	ProcessPayment(ctx context.Context, req *PaymentRequest) (*PaymentResult, error)
}

type Behavior string

const (
	BehaviorAlwaysSucceed    Behavior = "always_succeed"
	BehaviorAlwaysFail       Behavior = "always_fail"
	BehaviorTimeout          Behavior = "timeout"
	BehaviorUnknown          Behavior = "unknown"
	BehaviorDelayedSuccess   Behavior = "delayed_success"
	BehaviorError503         Behavior = "error_503"
	BehaviorNetworkError     Behavior = "network_error"
	BehaviorRandomFail       Behavior = "random_fail"
)

type MockProvider struct {
	providerID      string
	behaviors       map[Behavior]int
	defaultBehavior Behavior
	delay           time.Duration
	mu              sync.Mutex
	callCount       int
	results         []*PaymentResult
}

type MockProviderConfig struct {
	ProviderID      string
	DefaultBehavior Behavior
	Delay           time.Duration
	Behaviors       map[Behavior]int
}

func NewMockProvider(cfg MockProviderConfig) *MockProvider {
	if cfg.ProviderID == "" {
		cfg.ProviderID = "mock-provider"
	}
	if cfg.DefaultBehavior == "" {
		cfg.DefaultBehavior = BehaviorAlwaysSucceed
	}
	if cfg.Delay == 0 {
		cfg.Delay = 100 * time.Millisecond
	}
	return &MockProvider{
		providerID:      cfg.ProviderID,
		defaultBehavior: cfg.DefaultBehavior,
		delay:           cfg.Delay,
		behaviors:       cfg.Behaviors,
	}
}

func (m *MockProvider) ProcessPayment(ctx context.Context, req *PaymentRequest) (*PaymentResult, error) {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()

	behavior := m.resolveBehavior()

	switch behavior {
	case BehaviorAlwaysSucceed, BehaviorDelayedSuccess:
		if behavior == BehaviorDelayedSuccess {
			select {
			case <-time.After(m.delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		} else {
			select {
			case <-time.After(m.delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		result := &PaymentResult{
			ProviderID:     m.providerID,
			TransactionRef: fmt.Sprintf("TXN-%s-%d", req.PaymentID[:8], time.Now().UnixNano()),
			Status:         "SUCCESS",
			Message:        "Payment processed successfully",
			ResponseCode:   "00",
			ProcessedAt:    time.Now().UTC(),
		}
		m.recordResult(result)
		return result, nil

	case BehaviorAlwaysFail:
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		result := &PaymentResult{
			ProviderID:     m.providerID,
			TransactionRef: fmt.Sprintf("TXN-%s-%d", req.PaymentID[:8], time.Now().UnixNano()),
			Status:         "FAILED",
			Message:        "Insufficient funds",
			ResponseCode:   "51",
			ProcessedAt:    time.Now().UTC(),
		}
		m.recordResult(result)
		return result, nil

	case BehaviorTimeout:
		select {
		case <-time.After(30 * time.Second):
			return nil, fmt.Errorf("provider timeout after 30s")
		case <-ctx.Done():
			return nil, ctx.Err()
		}

	case BehaviorUnknown:
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		result := &PaymentResult{
			ProviderID:     m.providerID,
			TransactionRef: "",
			Status:         "UNKNOWN",
			Message:        "Partial response received",
			ResponseCode:   "",
			ProcessedAt:    time.Now().UTC(),
		}
		m.recordResult(result)
		return result, nil

	case BehaviorError503:
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("503 service unavailable")

	case BehaviorNetworkError:
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("connection refused: network unreachable")

	case BehaviorRandomFail:
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if rand.Intn(2) == 0 {
			result := &PaymentResult{
				ProviderID:     m.providerID,
				TransactionRef: fmt.Sprintf("TXN-%s-%d", req.PaymentID[:8], time.Now().UnixNano()),
				Status:         "SUCCESS",
				Message:        "Payment processed successfully",
				ResponseCode:   "00",
				ProcessedAt:    time.Now().UTC(),
			}
			m.recordResult(result)
			return result, nil
		}
		result := &PaymentResult{
			ProviderID:     m.providerID,
			TransactionRef: fmt.Sprintf("TXN-%s-%d", req.PaymentID[:8], time.Now().UnixNano()),
			Status:         "FAILED",
			Message:        "Random failure",
			ResponseCode:   "96",
			ProcessedAt:    time.Now().UTC(),
		}
		m.recordResult(result)
		return result, nil

	default:
		return nil, fmt.Errorf("unknown behavior: %s", behavior)
	}
}

func (m *MockProvider) resolveBehavior() Behavior {
	if m.behaviors == nil || len(m.behaviors) == 0 {
		return m.defaultBehavior
	}

	total := 0
	for _, count := range m.behaviors {
		total += count
	}

	if total == 0 {
		return m.defaultBehavior
	}

	r := rand.Intn(total)
	cumulative := 0
	for behavior, count := range m.behaviors {
		cumulative += count
		if r < cumulative {
			return behavior
		}
	}

	return m.defaultBehavior
}

func (m *MockProvider) recordResult(result *PaymentResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results = append(m.results, result)
}

func (m *MockProvider) GetCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

func (m *MockProvider) GetResults() []*PaymentResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*PaymentResult, len(m.results))
	copy(result, m.results)
	return result
}

func (m *MockProvider) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount = 0
	m.results = nil
}

func (m *MockProvider) SetDefaultBehavior(behavior Behavior) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defaultBehavior = behavior
}

func (m *MockProvider) SetBehaviors(behaviors map[Behavior]int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.behaviors = behaviors
}

func (m *MockProvider) SetDelay(delay time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.delay = delay
}
