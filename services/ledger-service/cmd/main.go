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

	"github.com/nexora/nexora/services/ledger-service/internal/events"
	"github.com/nexora/nexora/services/ledger-service/internal/ledgerguard"
	"github.com/nexora/nexora/services/ledger-service/internal/multicurrency"
	"github.com/nexora/nexora/services/ledger-service/internal/repository"
	"github.com/nexora/nexora/services/ledger-service/internal/service"
	"github.com/nexora/nexora/services/ledger-service/internal/transport"
	"github.com/nexora/nexora/shared/auth"
	"github.com/nexora/nexora/shared/config"
	"github.com/nexora/nexora/shared/health"
	"github.com/nexora/nexora/shared/shedding"
	"github.com/nexora/nexora/shared/telemetry"

	"github.com/nexora/nexora/services/ledger-service/internal/clients"
)

func main() {
	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).
		With().Timestamp().Logger()

	cfg := config.LoadServiceConfig("ledger-service", 8084, 8184)

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

	ledgerRepo := repository.NewCassandraLedgerRepository(session)

	producer, err := events.NewKafkaProducer(cfg.Kafka.Brokers, "nexora.ledger.events", logger)
	if err != nil {
		logger.Warn().Err(err).Msg("failed to connect to Kafka, continuing without event publishing")
	}
	if producer != nil {
		defer producer.Close()
	}

	ledgerService := service.NewLedgerService(ledgerRepo, producer, logger)

	// Ledger invariant monitor: continuous balance-chain + recompute sweeps.
	// On violation the monitor freezes the account and files a SEV1 incident
	// (incident-service is optional — freeze + event trail always work).
	ledgerService.SetIncidentReporter(clients.NewIncidentClient())
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		ledgerService.ScanAllAccountsIntegrity(context.Background()) // sweep once at boot
		for {
			select {
			case <-ticker.C:
				ledgerService.ScanAllAccountsIntegrity(context.Background())
			}
		}
	}()

	paymentProcessor := events.NewPaymentEventProcessor(ledgerService, ledgerRepo, logger)
	consumerTopics := []string{"nexora.payment.confirmed", "nexora.payment.settled"}
	// -ledger-v2: fresh group id so the fixed consumer replays from the oldest
	// offset (the previous group had committed offsets for events it could not
	// parse because they are published as envelopes).
	consumer, err := events.NewKafkaConsumer(cfg.Kafka.Brokers, cfg.Kafka.GroupID+"-ledger-v2", consumerTopics, paymentProcessor.HandlePaymentEvent, logger)
	if err != nil {
		logger.Warn().Err(err).Msg("failed to create Kafka consumer, continuing without event consumption")
	} else {
		consumer.Start(context.Background())
		logger.Info().Strs("topics", consumerTopics).Msg("Kafka payment consumer started")
	}

	handlers := transport.NewHandlers(ledgerService, logger)
	router := mux.NewRouter()
	handlers.RegisterRoutes(router)

	guardService := ledgerguard.NewService()
	guardHandlers := ledgerguard.NewHandlers(guardService, logger)
	guardHandlers.RegisterRoutes(router)

	mcService := multicurrency.NewService(logger)
	mcHandlers := multicurrency.NewHandlers(mcService, logger)
	mcHandlers.RegisterRoutes(router)

	// Adaptive load shedding: the ledger is the last line of defence, so it
	// sheds exploratory traffic first when dependencies degrade.
	controlURL := os.Getenv("CONTROL_PLANE_SERVICE_URL")
	if controlURL == "" {
		controlURL = "http://localhost:8096"
	}
	shedder := shedding.New("ledger-service", controlURL, nil)
	shedCtx, cancelShed := context.WithCancel(context.Background())
	defer cancelShed()
	shedder.Start(shedCtx)
	router.Use(shedder.Handler)

	router.Use(auth.NewAuthenticator(cfg.Auth.JWTSecret, os.Getenv("INTERNAL_TOKEN"), "/v1/health", "/metrics").Middleware)

	// Prometheus /metrics (shared/telemetry). Route-specific request
	// counters + latency histograms are exposed for Prometheus/Grafana.
	metricsRegistry := telemetry.NewRegistry()
	router.HandleFunc("/metrics", telemetry.MetricsHandler(metricsRegistry)).Methods("GET")
	router.Use(telemetry.HTTPMetrics(metricsRegistry, "ledger-service"))

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
	if consumer != nil {
		if err := consumer.Close(); err != nil {
			logger.Error().Err(err).Msg("Kafka consumer shutdown error")
		}
	}
	if err := healthServer.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("health server shutdown error")
	}
	logger.Info().Msg("server stopped")
}
