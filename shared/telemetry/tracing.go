package telemetry

import (
	"context"
	"sync"
	"time"
)

type Tracer struct {
	serviceName string
	endpoint    string
	spans       map[string]*Span
	mu          sync.RWMutex
}

type Span struct {
	TraceID    string            `json:"trace_id"`
	SpanID     string            `json:"span_id"`
	ParentID   string            `json:"parent_id,omitempty"`
	Service    string            `json:"service"`
	Operation  string            `json:"operation"`
	StartTime  time.Time         `json:"start_time"`
	EndTime    *time.Time        `json:"end_time,omitempty"`
	DurationMs float64           `json:"duration_ms,omitempty"`
	Status     string            `json:"status"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Events     []SpanEvent       `json:"events,omitempty"`
}

type SpanEvent struct {
	Name       string            `json:"name"`
	Timestamp  time.Time         `json:"timestamp"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

func InitTracer(serviceName, endpoint string) *Tracer {
	return &Tracer{
		serviceName: serviceName,
		endpoint:    endpoint,
		spans:       make(map[string]*Span),
	}
}

func (t *Tracer) CreateSpan(ctx context.Context, traceID, spanID, parentID, operation string) *Span {
	span := &Span{
		TraceID:    traceID,
		SpanID:     spanID,
		ParentID:   parentID,
		Service:    t.serviceName,
		Operation:  operation,
		StartTime:  time.Now().UTC(),
		Status:     "OK",
		Attributes: make(map[string]string),
		Events:     make([]SpanEvent, 0),
	}

	t.mu.Lock()
	t.spans[spanID] = span
	t.mu.Unlock()

	return span
}

func (t *Tracer) StartSpan(ctx context.Context, traceID, parentID, operation string) *Span {
	spanID := generateSpanID()
	return t.CreateSpan(ctx, traceID, spanID, parentID, operation)
}

func (t *Tracer) EndSpan(span *Span) {
	span.mu.Lock()
	defer span.mu.Unlock()

	now := time.Now().UTC()
	span.EndTime = &now
	span.DurationMs = float64(now.Sub(span.StartTime).Milliseconds())
}

func (t *Tracer) AddEvent(span *Span, name string, attributes map[string]string) {
	span.mu.Lock()
	defer span.mu.Unlock()

	event := SpanEvent{
		Name:       name,
		Timestamp:  time.Now().UTC(),
		Attributes: attributes,
	}
	span.Events = append(span.Events, event)
}

func (t *Tracer) SetAttribute(span *Span, key, value string) {
	span.mu.Lock()
	defer span.mu.Unlock()
	span.Attributes[key] = value
}

func (t *Tracer) SetStatus(span *Span, status string) {
	span.mu.Lock()
	defer span.mu.Unlock()
	span.Status = status
}

func (t *Tracer) GetSpan(spanID string) *Span {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.spans[spanID]
}

func (t *Tracer) GetSpansByTrace(traceID string) []*Span {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var result []*Span
	for _, span := range t.spans {
		if span.TraceID == traceID {
			result = append(result, span)
		}
	}
	return result
}

func (t *Tracer) GetAllSpans() []*Span {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make([]*Span, 0, len(t.spans))
	for _, span := range t.spans {
		result = append(result, span)
	}
	return result
}

func (t *Tracer) PropagateContext(ctx context.Context, span *Span) context.Context {
	ctx = context.WithValue(ctx, "trace_id", span.TraceID)
	ctx = context.WithValue(ctx, "span_id", span.SpanID)
	ctx = context.WithValue(ctx, "parent_id", span.ParentID)
	return ctx
}

func (t *Tracer) ExtractTraceID(ctx context.Context) string {
	if val := ctx.Value("trace_id"); val != nil {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func (t *Tracer) ExtractSpanID(ctx context.Context) string {
	if val := ctx.Value("span_id"); val != nil {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func generateSpanID() string {
	n := time.Now().UnixNano()
	return fmt.Sprintf("span-%d", n%1000000)
}

type TracingContextKey string

const (
	TraceIDKey    TracingContextKey = "trace_id"
	SpanIDKey     TracingContextKey = "span_id"
	ParentIDKey   TracingContextKey = "parent_id"
	ServiceNameKey TracingContextKey = "service_name"
)
