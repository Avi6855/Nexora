package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gocql/gocql"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/payment-service/internal/clients"
	"github.com/nexora/nexora/services/payment-service/internal/events"
	"github.com/nexora/nexora/services/payment-service/internal/idem"
	"github.com/nexora/nexora/services/payment-service/internal/moneymove"
	"github.com/nexora/nexora/services/payment-service/internal/paycycle"
	"github.com/nexora/nexora/services/payment-service/internal/provider"
	"github.com/nexora/nexora/services/payment-service/internal/repository"
	"github.com/nexora/nexora/services/payment-service/internal/service"
	"github.com/nexora/nexora/services/payment-service/internal/transport"
	"github.com/nexora/nexora/services/payment-service/internal/txpolicy"
	"github.com/nexora/nexora/shared/auth"
	"github.com/nexora/nexora/shared/config"
	"github.com/nexora/nexora/shared/health"
	"github.com/nexora/nexora/shared/outbox"
	"github.com/nexora/nexora/shared/shedding"
	"github.com/nexora/nexora/shared/telemetry"
)

func main() {
	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).
		With().Timestamp().Logger()

	cfg := config.LoadServiceConfig("payment-service", 8085, 8185)

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

	mockProvider := provider.NewMockProvider(provider.MockProviderConfig{
		ProviderID:      "mock-payment-provider",
		DefaultBehavior: provider.BehaviorAlwaysSucceed,
		Delay:           500 * time.Millisecond,
	})

	// ── Transactional outbox (ADR-005) ──────────────────────────────────
	// Every payment state transition is written to the outbox atomically with
	// the payments row (single logged batch); the relay below drains the
	// outbox to Kafka with retry + DLQ. No event can be lost because Kafka is
	// unavailable, and no event can be published for a transition that never
	// happened.
	outboxStore := outbox.NewCassandraStore(session)

	kafkaPublisher, err := outbox.NewKafkaPublisher(outbox.KafkaPublisherConfig{
		Brokers: cfg.Kafka.Brokers,
		Logger:  logger,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to create outbox kafka publisher")
	}

	relay := outbox.NewRelay(outboxStore, kafkaPublisher, logger, outbox.DefaultRelayConfig())
	relay.Start(context.Background())

	eventPublisher := events.NewOutboxEventPublisher(outboxStore, "payment-service", cfg.Kafka.TopicPrefix)

	paymentRepo := repository.NewCassandraPaymentRepository(session)
	paymentService := service.NewPaymentService(paymentRepo, mockProvider, eventPublisher, logger)

	// ── Security slice: real-time scam intelligence + lockdown ──────────
	// Outbound payments are evaluated by the fraud service over the user's
	// real decision history before any state is persisted, and refused when
	// the source account is in emergency lockdown. Both clients are nil in
	// environments without the sibling services (tests, local runs).
	if fraudClient := clients.NewFraudClient(); fraudClient != nil {
		paymentService.SetFraudClient(fraudClient)
		logger.Info().Msg("scam-intelligence gate enabled (fraud-service)")
	}
	paymentService.SetAccountClient(clients.NewAccountClient())

	handlers := transport.NewHandlers(paymentService, logger)
	router := mux.NewRouter()
	handlers.RegisterRoutes(router)

	// Pay-cycle platform (shared/paycycle): rail cut-off/holiday ETA quotes,
	// the beneficiary trust lifecycle and approval chains.
	paycycleLoc, err := time.LoadLocation("Europe/London")
	if err != nil {
		paycycleLoc = time.UTC
	}
	paycycleService := paycycle.NewService(paycycleLoc)
	paycycle.NewHandlers(paycycleService, logger).RegisterRoutes(router)

	// Dynamic transaction policy (shared/txpolicy): versioned screening
	// rules, evaluation, shadow simulation and the audit log.
	txpolicyService := txpolicy.NewService()
	txpolicy.NewHandlers(txpolicyService, logger).RegisterRoutes(router)

	// Idempotent API platform (shared/idempotency gateway): dedupe by key,
	// replay stored responses, re-execute after TTL expiry.
	idemSvc := idem.NewService(24*time.Hour, logger)
	idem.NewHandlers(idemSvc, logger).RegisterRoutes(router)
	logger.Info().Msg("idempotent API platform wired (/v1/idem)")

	// Money-correctness platform (shared/moneymove): intent ledger with
	// divergence flags, versioned instructions, precondition checks, atomic
	// reservations, fencing leases and the overdraft coordinator.
	moneymoveSvc := moneymove.NewService(logger)
	moneymove.NewHandlers(moneymoveSvc, logger).RegisterRoutes(router)
	logger.Info().Msg("money-move platform wired (/v1/money-move)")

	// Adaptive load shedding: poll the control plane's dependency graph; when
	// a critical dependency degrades, non-critical traffic is shed first so
	// payments stay alive (shared/shedding).
	controlURL := os.Getenv("CONTROL_PLANE_SERVICE_URL")
	if controlURL == "" {
		controlURL = "http://localhost:8096"
	}
	shedder := shedding.New("payment-service", controlURL, nil)
	shedCtx, cancelShed := context.WithCancel(context.Background())
	defer cancelShed()
	shedder.Start(shedCtx)
	router.Use(shedder.Handler)

	router.Use(auth.NewAuthenticator(cfg.Auth.JWTSecret, os.Getenv("INTERNAL_TOKEN"), "/v1/health", "/metrics", "/v1/webhooks").Middleware)

	// Prometheus /metrics (shared/telemetry). Route-specific request
	// counters + latency histograms are exposed for Prometheus/Grafana.
	metricsRegistry := telemetry.NewRegistry()
	router.HandleFunc("/metrics", telemetry.MetricsHandler(metricsRegistry)).Methods("GET")
	router.Use(telemetry.HTTPMetrics(metricsRegistry, "payment-service"))
	router.HandleFunc("/health", handlers.HealthCheck).Methods("GET")

	healthAddr := fmt.Sprintf(":%d", cfg.Service.Port+100)
	healthServer := health.NewHealthServer(healthAddr)
	healthServer.RegisterCheck("cassandra", func(ctx context.Context) error {
		return session.Query("SELECT now()").WithContext(ctx).Exec()
	})

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Service.Port),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

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

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("HTTP server shutdown error")
	}
	if err := healthServer.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("health server shutdown error")
	}
	relay.Stop()
	if err := kafkaPublisher.Close(); err != nil {
		logger.Error().Err(err).Msg("outbox kafka publisher shutdown error")
	}
	if err := eventPublisher.Close(); err != nil {
		logger.Error().Err(err).Msg("event publisher shutdown error")
	}
	logger.Info().Msg("server stopped")
}
