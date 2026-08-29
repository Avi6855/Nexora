package flight_recorder

import (
	"sync"
	"time"
)

type Stage string

const (
	StageRequest       Stage = "REQUEST"
	StageAuthentication Stage = "AUTHENTICATION"
	StagePolicy        Stage = "POLICY"
	StageFraud         Stage = "FRAUD"
	StageLimits        Stage = "LIMITS"
	StageReservation   Stage = "RESERVATION"
	StageLedger        Stage = "LEDGER"
	StageKafka         Stage = "KAFKA"
	StageProvider      Stage = "PROVIDER"
	StageSettlement    Stage = "SETTLEMENT"
	StageNotification  Stage = "NOTIFICATION"
)

type EventStatus string

const (
	EventStatusSuccess EventStatus = "SUCCESS"
	EventStatusFailure EventStatus = "FAILURE"
	EventStatusPending EventStatus = "PENDING"
)

type FlightEvent struct {
	TraceID     string            `json:"trace_id"`
	SpanID      string            `json:"span_id"`
	Service     string            `json:"service"`
	Stage       Stage             `json:"stage"`
	Decision    string            `json:"decision"`
	Status      EventStatus       `json:"status"`
	LatencyMs   float64           `json:"latency_ms"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Timestamp   time.Time         `json:"timestamp"`
}

type TransactionTrace struct {
	TransactionID string        `json:"transaction_id"`
	Events        []*FlightEvent `json:"events"`
	TotalLatency  float64       `json:"total_latency_ms"`
	StartTime     time.Time     `json:"start_time"`
	EndTime       time.Time     `json:"end_time"`
	Success       bool          `json:"success"`
}

type FlightRecorder struct {
	events map[string][]*FlightEvent
	mu     sync.RWMutex
}

func NewFlightRecorder() *FlightRecorder {
	return &FlightRecorder{
		events: make(map[string][]*FlightEvent),
	}
}

func (r *FlightRecorder) Record(traceID, spanID, service string, stage Stage, decision string, status EventStatus, latencyMs float64, metadata map[string]string) {
	event := &FlightEvent{
		TraceID:   traceID,
		SpanID:    spanID,
		Service:   service,
		Stage:     stage,
		Decision:  decision,
		Status:    status,
		LatencyMs: latencyMs,
		Metadata:  metadata,
		Timestamp: time.Now().UTC(),
	}

	r.mu.Lock()
	r.events[traceID] = append(r.events[traceID], event)
	r.mu.Unlock()
}

func (r *FlightRecorder) GetTrace(transactionID string) *TransactionTrace {
	r.mu.RLock()
	events, ok := r.events[transactionID]
	r.mu.RUnlock()

	if !ok || len(events) == 0 {
		return nil
	}

	sorted := make([]*FlightEvent, len(events))
	copy(sorted, events)

	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].Timestamp.Before(sorted[i].Timestamp) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	var totalLatency float64
	for _, e := range sorted {
		totalLatency += e.LatencyMs
	}

	allSuccess := true
	for _, e := range sorted {
		if e.Status == EventStatusFailure {
			allSuccess = false
			break
		}
	}

	trace := &TransactionTrace{
		TransactionID: transactionID,
		Events:        sorted,
		TotalLatency:  totalLatency,
		StartTime:     sorted[0].Timestamp,
		EndTime:       sorted[len(sorted)-1].Timestamp,
		Success:       allSuccess,
	}

	return trace
}

func (r *FlightRecorder) GetAllTraces() map[string]*TransactionTrace {
	r.mu.RLock()
	defer r.mu.RUnlock()

	traces := make(map[string]*TransactionTrace)
	for traceID := range r.events {
		events := make([]*FlightEvent, len(r.events[traceID]))
		copy(events, r.events[traceID])

		var totalLatency float64
		for _, e := range events {
			totalLatency += e.LatencyMs
		}

		allSuccess := true
		for _, e := range events {
			if e.Status == EventStatusFailure {
				allSuccess = false
				break
			}
		}

		traces[traceID] = &TransactionTrace{
			TransactionID: traceID,
			Events:        events,
			TotalLatency:  totalLatency,
			StartTime:     events[0].Timestamp,
			EndTime:       events[len(events)-1].Timestamp,
			Success:       allSuccess,
		}
	}

	return traces
}

func (r *FlightRecorder) GetEventsByStage(stage Stage) []*FlightEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*FlightEvent
	for _, events := range r.events {
		for _, e := range events {
			if e.Stage == stage {
				result = append(result, e)
			}
		}
	}
	return result
}

func (r *FlightRecorder) GetFailedTraces() []*TransactionTrace {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var failed []*TransactionTrace
	for traceID, events := range r.events {
		allSuccess := true
		for _, e := range events {
			if e.Status == EventStatusFailure {
				allSuccess = false
				break
			}
		}

		if !allSuccess {
			var totalLatency float64
			for _, e := range events {
				totalLatency += e.LatencyMs
			}

			failed = append(failed, &TransactionTrace{
				TransactionID: traceID,
				Events:        events,
				TotalLatency:  totalLatency,
				StartTime:     events[0].Timestamp,
				EndTime:       events[len(events)-1].Timestamp,
				Success:       false,
			})
		}
	}

	return failed
}

func (r *FlightRecorder) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = make(map[string][]*FlightEvent)
}
