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

	"github.com/nexora/nexora/services/card-service/internal/clients"
	"github.com/nexora/nexora/services/card-service/internal/events"
	"github.com/nexora/nexora/services/card-service/internal/repository"
	"github.com/nexora/nexora/services/card-service/internal/service"
	"github.com/nexora/nexora/services/card-service/internal/transport"
	"github.com/nexora/nexora/shared/auth"
	"github.com/nexora/nexora/shared/config"
	"github.com/nexora/nexora/shared/health"
	"github.com/nexora/nexora/shared/telemetry"
)

func main() {
	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).With().Timestamp().Logger()
	cfg := config.LoadServiceConfig("card-service", 8087, 8187)

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

	publisher := events.NewKafkaEventPublisher(events.KafkaPublisherConfig{
		ProducerID:  "card-service",
		Brokers:     cfg.Kafka.Brokers,
		TopicPrefix: cfg.Kafka.TopicPrefix,
		Logger:      logger,
	})
	defer publisher.Close()

	cardRepo := repository.NewCassandraCardRepository(session)
	authRepo := repository.NewCassandraAuthorizationRepository(session)
	cardService := service.NewCardService(cardRepo, publisher, logger)
	authService := service.NewAuthorizationService(
		cardRepo,
		authRepo,
		clients.NewFraudClient(clients.DefaultFraudURL(), logger),
		clients.NewLedgerClient(clients.DefaultLedgerURL(), logger),
		publisher,
		logger,
	)

	// Emergency-lockdown enforcement: card presentments against a locked
	// account decline immediately with account_locked.
	authService.SetAccountClient(clients.NewAccountClient())

	metricsRegistry := telemetry.NewRegistry()
	handlers := transport.NewHandlers(cardService, authService, logger, metricsRegistry)
	router := mux.NewRouter()
	handlers.RegisterRoutes(router)
	router.Use(auth.NewAuthenticator(cfg.Auth.JWTSecret, os.Getenv("INTERNAL_TOKEN"), "/v1/health", "/metrics").Middleware)

	// Prometheus /metrics (shared/telemetry). Route-specific request
	// counters + latency histograms are exposed for Prometheus/Grafana.
	router.HandleFunc("/metrics", telemetry.MetricsHandler(metricsRegistry)).Methods("GET")
	router.Use(telemetry.HTTPMetrics(metricsRegistry, "card-service"))

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
	httpServer.Shutdown(shutdownCtx)
	healthServer.Shutdown(shutdownCtx)
	logger.Info().Msg("server stopped")
}
