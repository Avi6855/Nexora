package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/reconciliation-service/internal/clients"
	"github.com/nexora/nexora/services/reconciliation-service/internal/events"
	"github.com/nexora/nexora/services/reconciliation-service/internal/reconnet"
	"github.com/nexora/nexora/services/reconciliation-service/internal/repository"
	"github.com/nexora/nexora/services/reconciliation-service/internal/service"
	"github.com/nexora/nexora/services/reconciliation-service/internal/transport"
	"github.com/nexora/nexora/shared/config"
	"github.com/nexora/nexora/shared/health"
)

func main() {
	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).With().Timestamp().Logger()
	cfg := config.LoadServiceConfig("reconciliation-service", 8091, 8191)

	cluster := gocql.NewCluster(cfg.Cassandra.Hosts...)
	cluster.Keyspace = cfg.Cassandra.Keyspace
	cluster.Consistency = gocql.Quorum
	cluster.Timeout = cfg.Cassandra.Timeout
	cluster.ConnectTimeout = 10 * time.Second

	var session *gocql.Session
	var err error
	for i := 0; i < 10; i++ {
		session, err = cluster.CreateSession()
		if err == nil {
			break
		}
		logger.Warn().Int("attempt", i+1).Err(err).Msg("failed to connect to Cassandra, retrying...")
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to connect to Cassandra")
	}
	defer session.Close()

	reconRepo := repository.NewCassandraReconciliationRepository(session)

	producer, err := events.NewKafkaProducer(cfg.Kafka.Brokers, "nexora.reconciliation.events", logger)
	if err != nil {
		logger.Warn().Err(err).Msg("failed to connect to Kafka, continuing without event publishing")
	}
	if producer != nil {
		defer producer.Close()
	}

	reconService := service.NewReconciliationService(reconRepo, producer, logger)

	// ── Outcome write-back (ADR-018) ─────────────────────────────────────
	// Reconciliation is only worth anything if its decision reaches the
	// payment: a case closed on paper while the payment stays UNKNOWN leaves
	// the customer's authorisation hold in place for a decision already taken.
	if paymentURL := os.Getenv("PAYMENT_SERVICE_URL"); paymentURL != "" {
		reconService.SetPaymentGateway(clients.NewPaymentClient())
		logger.Info().Str("payment_url", paymentURL).Msg("reconciliation write-back enabled (payment-service)")
	} else {
		logger.Warn().Msg("PAYMENT_SERVICE_URL not set: cases are still recorded and escalated, but outcomes cannot be written back to payments")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ── The UNKNOWN bucket is worked, not just filled (ADR-007) ──────────
	// Every payment that goes UNKNOWN gets a case the moment it happens, so
	// the sweep has something to work without anyone remembering to file it.
	unknownTopic := fmt.Sprintf("%s.payment.unknown", cfg.Kafka.TopicPrefix)
	unknownHandler := func(ctx context.Context, payload *events.PaymentEventPayload, eventType string) error {
		paymentID, err := uuid.Parse(payload.PaymentID)
		if err != nil {
			return fmt.Errorf("invalid payment id %q: %w", payload.PaymentID, err)
		}
		_, err = reconService.OpenCaseForUnknownPayment(ctx, paymentID, payload.Amount, payload.Currency)
		return err
	}
	consumer, err := events.NewKafkaConsumer(cfg.Kafka.Brokers, cfg.Kafka.GroupID+"-reconciliation", []string{unknownTopic}, unknownHandler, logger)
	if err != nil {
		logger.Warn().Err(err).Str("topic", unknownTopic).Msg("failed to create Kafka consumer; UNKNOWN payments will only be picked up by the sweep")
	} else {
		consumer.Start(ctx)
		defer consumer.Close()
		logger.Info().Str("topic", unknownTopic).Msg("payment.unknown consumer started")
	}

	// ── Reconciliation sweep ────────────────────────────────────────────
	// The sweep used to be an endpoint somebody had to remember to call. It now
	// runs on an interval, so an UNKNOWN payment is retried and then escalated
	// without a human watching for it.
	interval := 60 * time.Second
	if raw := os.Getenv("RECONCILIATION_INTERVAL"); raw != "" {
		if parsed, parseErr := time.ParseDuration(raw); parseErr != nil {
			logger.Warn().Err(parseErr).Str("value", raw).Msg("invalid RECONCILIATION_INTERVAL, using 60s")
		} else {
			interval = parsed
		}
	}
	batchSize := 50
	if raw := os.Getenv("RECONCILIATION_BATCH_SIZE"); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed > 0 {
			batchSize = parsed
		}
	}
	go reconService.RunScheduler(ctx, interval, batchSize)

	handlers := transport.NewHandlers(reconService, logger)
	router := mux.NewRouter()
	handlers.RegisterRoutes(router)

	reconNetSvc := reconnet.NewService()
	reconNetHandlers := reconnet.NewHandlers(reconNetSvc, logger)
	reconNetHandlers.RegisterRoutes(router)

	healthAddr := fmt.Sprintf(":%d", cfg.Service.Port+100)
	healthServer := health.NewHealthServer(healthAddr)
	healthServer.RegisterCheck("cassandra", func(ctx context.Context) error {
		return session.Query("SELECT now()").WithContext(ctx).Exec()
	})

	httpServer := &http.Server{Addr: fmt.Sprintf(":%d", cfg.Service.Port), Handler: router, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}

	go func() {
		logger.Info().Int("port", cfg.Service.Port).Msg("starting HTTP server")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("HTTP server error")
		}
	}()

	go func() {
		logger.Info().Str("addr", healthAddr).Msg("starting health server")
		if err := healthServer.Start(); err != nil && err != http.ErrServerClosed {
			logger.Error().Err(err).Msg("health server error")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info().Msg("shutting down server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	httpServer.Shutdown(shutdownCtx)
	healthServer.Shutdown(shutdownCtx)
	logger.Info().Msg("server stopped")
}
