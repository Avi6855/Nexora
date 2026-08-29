package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

var (
	ErrConflict         = errors.New("idempotency key already used with different request")
	ErrKeyRequired      = errors.New("idempotency key is required")
	ErrRecordNotFound   = errors.New("idempotency record not found")
)

type Status string

const (
	StatusPending    Status = "PENDING"
	StatusCompleted  Status = "COMPLETED"
	StatusFailed     Status = "FAILED"
)

type IdempotencyRecord struct {
	Key         string
	RequestHash string
	Status      Status
	Response    []byte
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ExpiresAt   time.Time
}

type IdempotencyStore interface {
	Create(ctx context.Context, record *IdempotencyRecord) error
	Get(ctx context.Context, key string) (*IdempotencyRecord, error)
	UpdateStatus(ctx context.Context, key string, status Status, response []byte) error
	DeleteExpired(ctx context.Context) error
}

type Engine struct {
	store IdempotencyStore
	ttl   time.Duration
}

func NewEngine(store IdempotencyStore, ttl time.Duration) *Engine {
	return &Engine{store: store, ttl: ttl}
}

func GenerateKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func HashRequest(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func (e *Engine) CheckOrCreate(ctx context.Context, key string, requestHash string) (*IdempotencyRecord, bool, error) {
	if key == "" {
		return nil, false, ErrKeyRequired
	}

	existing, err := e.store.Get(ctx, key)
	if err == nil && existing != nil {
		if existing.RequestHash != requestHash {
			return existing, false, ErrConflict
		}
		return existing, false, nil
	}

	record := &IdempotencyRecord{
		Key:         key,
		RequestHash: requestHash,
		Status:      StatusPending,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
		ExpiresAt:   time.Now().UTC().Add(e.ttl),
	}

	if err := e.store.Create(ctx, record); err != nil {
		return nil, false, fmt.Errorf("creating idempotency record: %w", err)
	}

	return record, true, nil
}

func (e *Engine) Complete(ctx context.Context, key string, response []byte) error {
	return e.store.UpdateStatus(ctx, key, StatusCompleted, response)
}

func (e *Engine) Fail(ctx context.Context, key string) error {
	return e.store.UpdateStatus(ctx, key, StatusFailed, nil)
}
