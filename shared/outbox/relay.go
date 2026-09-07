package outbox

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// RelayConfig tunes the outbox relay.
type RelayConfig struct {
	PollInterval time.Duration
	BatchSize    int
	MaxAttempts  int
}

func DefaultRelayConfig() RelayConfig {
	return RelayConfig{
		PollInterval: 1 * time.Second,
		BatchSize:    100,
		MaxAttempts:  5,
	}
}

// Relay drains the outbox: it claims PENDING events, publishes them to Kafka
// and marks them PUBLISHED. Failed publishes leave the event PENDING so the
// next poll retries it (attempt count increments via the store). An event
// whose attempts reach MaxAttempts is moved to the dead-letter topic and
// marked FAILED, so a poison event can never wedge the relay.
//
// The relay drains immediately on start, which is what makes it safe across
// restarts: any event enqueued but not yet published (or published but not
// yet marked) is picked up again. Delivery is at-least-once.
type Relay struct {
	store     Store
	publisher Publisher
	logger    zerolog.Logger
	cfg       RelayConfig

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewRelay(store Store, publisher Publisher, logger zerolog.Logger, cfg RelayConfig) *Relay {
	return &Relay{
		store:     store,
		publisher: publisher,
		logger:    logger,
		cfg:       cfg,
	}
}

// Start launches the relay loop. Call Stop to shut it down gracefully.
func (r *Relay) Start(parent context.Context) {
	r.ctx, r.cancel = context.WithCancel(parent)
	r.wg.Add(1)
	go r.run()
	r.logger.Info().
		Dur("poll_interval", r.cfg.PollInterval).
		Int("batch_size", r.cfg.BatchSize).
		Int("max_attempts", r.cfg.MaxAttempts).
		Msg("outbox relay started")
}

func (r *Relay) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	r.logger.Info().Msg("outbox relay stopped")
}

func (r *Relay) run() {
	defer r.wg.Done()

	// Drain immediately: recovers events left behind by a previous instance.
	r.drainOnce()

	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.drainOnce()
		}
	}
}

func (r *Relay) drainOnce() {
	events, err := r.store.ClaimPending(r.ctx, r.cfg.BatchSize)
	if err != nil {
		r.logger.Error().Err(err).Msg("outbox relay: failed to claim pending events")
		return
	}
	if len(events) == 0 {
		return
	}

	for _, ev := range events {
		r.process(r.ctx, ev)
	}
}

func (r *Relay) process(ctx context.Context, ev *Event) {
	if ev.Attempts >= r.cfg.MaxAttempts {
		// Terminal failure: deliver to the DLQ once, then stop retrying.
		if err := r.publisher.PublishDLQ(ctx, ev); err != nil {
			r.logger.Error().Err(err).
				Str("event_id", ev.EventID.String()).
				Str("topic", ev.Topic).
				Msg("outbox relay: DLQ publish failed; leaving event pending")
			return
		}
		if err := r.store.MarkTerminal(ctx, ev.EventID); err != nil {
			r.logger.Error().Err(err).
				Str("event_id", ev.EventID.String()).
				Msg("outbox relay: failed to mark event terminal")
		}
		r.logger.Warn().
			Str("event_id", ev.EventID.String()).
			Str("topic", ev.Topic).
			Int("attempts", ev.Attempts).
			Str("last_error", ev.LastError).
			Msg("outbox relay: event moved to DLQ")
		return
	}

	if err := r.publisher.Publish(ctx, ev); err != nil {
		if merr := r.store.MarkFailed(ctx, ev.EventID, err); merr != nil {
			r.logger.Error().Err(merr).
				Str("event_id", ev.EventID.String()).
				Msg("outbox relay: failed to record publish failure")
		}
		r.logger.Warn().Err(err).
			Str("event_id", ev.EventID.String()).
			Str("topic", ev.Topic).
			Int("attempts", ev.Attempts+1).
			Msg("outbox relay: publish failed, will retry")
		return
	}

	if err := r.store.MarkPublished(ctx, ev.EventID); err != nil {
		// Delivered but not marked: a crash here means at-least-once delivery.
		// Consumers deduplicate on event_id, so this is safe.
		r.logger.Error().Err(err).
			Str("event_id", ev.EventID.String()).
			Msg("outbox relay: delivered but failed to mark published; consumer dedupe required")
		return
	}

	r.logger.Info().
		Str("event_id", ev.EventID.String()).
		Str("topic", ev.Topic).
		Str("aggregate_id", ev.AggregateID).
		Msg("outbox relay: published")
}