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

	"github.com/nexora/nexora/services/pot-service/internal/clients"
	"github.com/nexora/nexora/services/pot-service/internal/events"
	"github.com/nexora/nexora/services/pot-service/internal/invest"
	"github.com/nexora/nexora/services/pot-service/internal/repository"
	"github.com/nexora/nexora/services/pot-service/internal/service"
	"github.com/nexora/nexora/services/pot-service/internal/transport"
	"github.com/nexora/nexora/shared/auth"
	"github.com/nexora/nexora/shared/config"
	"github.com/nexora/nexora/shared/health"
)

func main() {
	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).With().Timestamp().Logger()
	cfg := config.LoadServiceConfig("pot-service", 8088, 8188)

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
		ProducerID:  "pot-service",
		Brokers:     cfg.Kafka.Brokers,
		TopicPrefix: cfg.Kafka.TopicPrefix,
		Logger:      logger,
	})
	defer publisher.Close()

	potRepo := repository.NewCassandraPotRepository(session)
	potService := service.NewPotService(potRepo, publisher, clients.NewLedgerClient(), clients.NewAccountClient(), logger)

	// Roundups: sweep spare change from captured card authorizations into the
	// user's round-up-enabled pot. Exactly-once via the roundup_processed LWT.
	roundupRepo := repository.NewCassandraRoundupRepository(session)
	roundupConsumer := events.NewRoundupConsumer(potService, roundupRepo, logger)
	roundupTopics := []string{"nexora.card.authorization.captured"}
	kafkaConsumer, err := events.NewKafkaConsumer(cfg.Kafka.Brokers, cfg.Kafka.GroupID+"-roundups", roundupTopics, roundupConsumer.HandleRoundupEvent, logger)
	if err != nil {
		logger.Warn().Err(err).Msg("failed to create Kafka consumer, roundups disabled")
	} else {
		kafkaConsumer.Start(context.Background())
		logger.Info().Strs("topics", roundupTopics).Msg("roundups Kafka consumer started")
	}

	handlers := transport.NewHandlers(potService, logger)
	router := mux.NewRouter()
	handlers.RegisterRoutes(router)
	investService := invest.NewService()
	investHandlers := invest.NewHandlers(investService, logger)
	investHandlers.RegisterRoutes(router)
	router.Use(auth.NewAuthenticator(cfg.Auth.JWTSecret, os.Getenv("INTERNAL_TOKEN"), "/v1/health", "/metrics").Middleware)

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
	if kafkaConsumer != nil {
		if err := kafkaConsumer.Close(); err != nil {
			logger.Error().Err(err).Msg("roundups consumer shutdown error")
		}
	}
	logger.Info().Msg("server stopped")
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
