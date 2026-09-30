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
	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/transfer-service/internal/clients"
	"github.com/nexora/nexora/services/transfer-service/internal/events"
	"github.com/nexora/nexora/services/transfer-service/internal/fxtrack"
	"github.com/nexora/nexora/services/transfer-service/internal/offlineops"
	"github.com/nexora/nexora/services/transfer-service/internal/repository"
	"github.com/nexora/nexora/services/transfer-service/internal/service"
	"github.com/nexora/nexora/services/transfer-service/internal/transport"
	"github.com/nexora/nexora/shared/auth"
	"github.com/nexora/nexora/shared/config"
	"github.com/nexora/nexora/shared/health"
)

// durationFromEnv reads a duration setting, falling back to the default when it
// is missing or unparseable rather than refusing to start.
func durationFromEnv(key string, fallback time.Duration, logger zerolog.Logger) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		logger.Warn().Err(err).Str("value", raw).Str("key", key).Msg("invalid duration, using the default")
		return fallback
	}
	return parsed
}

func intFromEnv(key string, fallback int, logger zerolog.Logger) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		logger.Warn().Str("value", raw).Str("key", key).Msg("invalid number, using the default")
		return fallback
	}
	return parsed
}

func main() {
	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).With().Timestamp().Logger()
	cfg := config.LoadServiceConfig("transfer-service", 8086, 8186)

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

	transferRepo := repository.NewCassandraTransferRepository(session)

	producer, err := events.NewKafkaProducer(cfg.Kafka.Brokers, "nexora.transfer.events", logger)
	if err != nil {
		logger.Warn().Err(err).Msg("failed to connect to Kafka, continuing without event publishing")
	}
	if producer != nil {
		defer producer.Close()
	}

	transferService := service.NewTransferService(transferRepo, clients.NewLedgerClient(), clients.NewAccountClient(), producer, logger)

	// ── Indeterminate transfers (ADR-007) ───────────────────────────────
	// A booking that timed out is not a booking that was refused. Transfers in
	// that state are retried against the ledger on an interval — the ledger
	// booking is idempotent on the transfer's own key, so a retry settles the
	// booking that already happened or books the one that never did, and after
	// the escalation window a human is asked to look instead.
	transferService.SetEscalationWindow(durationFromEnv("TRANSFER_SWEEP_ESCALATE_AFTER", 15*time.Minute, logger))
	sweepCtx, cancelSweep := context.WithCancel(context.Background())
	defer cancelSweep()
	go transferService.RunUnknownScheduler(
		sweepCtx,
		durationFromEnv("TRANSFER_SWEEP_INTERVAL", time.Minute, logger),
		intFromEnv("TRANSFER_SWEEP_BATCH_SIZE", 50, logger),
	)

	handlers := transport.NewHandlers(transferService, logger)
	router := mux.NewRouter()
	handlers.RegisterRoutes(router)

	offlineService := offlineops.NewService()
	offlineHandlers := offlineops.NewHandlers(offlineService, logger)
	offlineHandlers.RegisterRoutes(router)

	fxService := fxtrack.NewService(logger)
	fxHandlers := fxtrack.NewHandlers(fxService, logger)
	fxHandlers.RegisterRoutes(router)
	router.Use(auth.NewAuthenticator(cfg.Auth.JWTSecret, os.Getenv("INTERNAL_TOKEN"), "/v1/health", "/metrics").Middleware)

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
