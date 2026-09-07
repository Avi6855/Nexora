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

	"github.com/nexora/nexora/services/insights-service/internal/clients"
	"github.com/nexora/nexora/services/insights-service/internal/events"
	"github.com/nexora/nexora/services/insights-service/internal/repository"
	"github.com/nexora/nexora/services/insights-service/internal/service"
	"github.com/nexora/nexora/services/insights-service/internal/transport"
	"github.com/nexora/nexora/shared/auth"
	"github.com/nexora/nexora/shared/config"
	"github.com/nexora/nexora/shared/health"
	"github.com/nexora/nexora/shared/telemetry"
)

func main() {
	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).
		With().Timestamp().Logger()

	cfg := config.LoadServiceConfig("insights-service", 8099, 8199)

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

	// Kafka alert publisher (optional: the service still computes insights if
	// Kafka is down, only the push fan-out is lost).
	alertProducer, err := events.NewAlertProducer(cfg.Kafka.Brokers, "nexora.insights.alerts", logger)
	if err != nil {
		logger.Warn().Err(err).Msg("failed to connect to Kafka, insights alerts will be stored but not pushed")
		alertProducer = nil
	} else {
		defer alertProducer.Close()
	}

	repo := repository.NewCassandraRepository(session)
	ledger := clients.NewLedgerClient()
	intelligence := service.NewIntelligenceService(repo, ledger, alertProducer, logger)

	// Event ingestion: captured card spend + settled payments in.
	consumerTopics := []string{
		"nexora.card.authorization.captured",
		"nexora.payment.confirmed",
		"nexora.payment.settled",
	}
	consumer := events.NewInsightsConsumer(
		intelligence.RecordSpend,
		intelligence.RecordIncome,
		intelligence.CheckAnomaly,
		logger,
	)
	group, err := events.NewKafkaGroup(cfg.Kafka.Brokers, cfg.Kafka.GroupID+"-insights", consumerTopics, consumer.HandleEvent, logger)
	if err != nil {
		logger.Warn().Err(err).Msg("failed to create Kafka consumer, insights will not ingest events")
	} else {
		group.Start(context.Background())
		logger.Info().Strs("topics", consumerTopics).Msg("insights Kafka consumer started")
	}

	// Periodic income-lifecycle checks: late-salary detection runs every
	// 6h; a full scan is cheap (one small Cassandra table).
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		intelligence.CheckSalaryLate(context.Background()) // once at boot too
		for {
			select {
			case <-ticker.C:
				intelligence.CheckSalaryLate(context.Background())
			case <-context.Background().Done():
				return
			}
		}
	}()

	handlers := transport.NewHandlers(intelligence, logger)
	router := mux.NewRouter()
	handlers.RegisterRoutes(router)
	router.Use(auth.NewAuthenticator(cfg.Auth.JWTSecret, os.Getenv("INTERNAL_TOKEN"), "/v1/insights/health", "/metrics").Middleware)

	metricsRegistry := telemetry.NewRegistry()
	router.HandleFunc("/metrics", telemetry.MetricsHandler(metricsRegistry)).Methods("GET")
	router.Use(telemetry.HTTPMetrics(metricsRegistry, "insights-service"))

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

	logger.Info().Msg("shutting down insights-service...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("HTTP server shutdown error")
	}
	if group != nil {
		if err := group.Close(); err != nil {
			logger.Error().Err(err).Msg("Kafka consumer shutdown error")
		}
	}
	if err := healthServer.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("health server shutdown error")
	}
	logger.Info().Msg("insights-service stopped")
}
