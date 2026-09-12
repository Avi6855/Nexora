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

	consentaigw "github.com/nexora/nexora/services/consent-service/internal/aigateway"
	"github.com/nexora/nexora/services/consent-service/internal/consentcentre"
	"github.com/nexora/nexora/services/consent-service/internal/delegated"
	consentob "github.com/nexora/nexora/services/consent-service/internal/openbanking"
	"github.com/nexora/nexora/services/consent-service/internal/repository"
	"github.com/nexora/nexora/services/consent-service/internal/service"
	"github.com/nexora/nexora/services/consent-service/internal/transport"
	"github.com/nexora/nexora/shared/auth"
	"github.com/nexora/nexora/shared/config"
	"github.com/nexora/nexora/shared/health"
	"github.com/nexora/nexora/shared/telemetry"
)

func main() {
	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).
		With().Timestamp().Logger()

	cfg := config.LoadServiceConfig("consent-service", 8097, 8197)

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

	repo := repository.NewCassandraRepository(session)
	consent := service.NewConsentService(repo, logger)

	handlers := transport.NewHandlers(consent, logger)
	router := mux.NewRouter()
	handlers.RegisterRoutes(router)
	obSvc := consentob.NewService()
	obHandlers := consentob.NewHandlers(obSvc, logger)
	obHandlers.RegisterRoutes(router)
	aiSvc := consentaigw.NewService(logger)
	aiHandlers := consentaigw.NewHandlers(aiSvc, logger)
	aiHandlers.RegisterRoutes(router)
	ccSvc := consentcentre.NewService(logger)
	ccHandlers := consentcentre.NewHandlers(ccSvc, logger)
	ccHandlers.RegisterRoutes(router)
	delSvc := delegated.NewService(logger)
	delHandlers := delegated.NewHandlers(delSvc, logger)
	delHandlers.RegisterRoutes(router)
	router.Use(auth.NewAuthenticator(cfg.Auth.JWTSecret, os.Getenv("INTERNAL_TOKEN"), "/v1/consent/health", "/metrics").Middleware)

	metricsRegistry := telemetry.NewRegistry()
	router.HandleFunc("/metrics", telemetry.MetricsHandler(metricsRegistry)).Methods("GET")
	router.Use(telemetry.HTTPMetrics(metricsRegistry, "consent-service"))

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

	logger.Info().Msg("shutting down consent-service...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("HTTP server shutdown error")
	}
	if err := healthServer.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("health server shutdown error")
	}
	logger.Info().Msg("consent-service stopped")
}
