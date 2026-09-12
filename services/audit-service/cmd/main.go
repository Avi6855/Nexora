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

	"github.com/nexora/nexora/services/audit-service/internal/complianceops"
	"github.com/nexora/nexora/services/audit-service/internal/dataline"
	"github.com/nexora/nexora/services/audit-service/internal/docintel"
	"github.com/nexora/nexora/services/audit-service/internal/events"
	"github.com/nexora/nexora/services/audit-service/internal/repository"
	"github.com/nexora/nexora/services/audit-service/internal/service"
	"github.com/nexora/nexora/services/audit-service/internal/taxpack"
	"github.com/nexora/nexora/services/audit-service/internal/transport"
	"github.com/nexora/nexora/shared/config"
	"github.com/nexora/nexora/shared/health"
)

func main() {
	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).With().Timestamp().Logger()
	cfg := config.LoadServiceConfig("audit-service", 8095, 8195)

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

	auditRepo := repository.NewCassandraAuditRepository(session)

	producer, err := events.NewKafkaProducer(cfg.Kafka.Brokers, "nexora.audit.events", logger)
	if err != nil {
		logger.Warn().Err(err).Msg("failed to connect to Kafka, continuing without event publishing")
	}
	if producer != nil {
		defer producer.Close()
	}

	auditService := service.NewAuditService(auditRepo, producer, logger)

	handlers := transport.NewHandlers(auditService, logger)
	router := mux.NewRouter()
	handlers.RegisterRoutes(router)
	dataService := dataline.NewService()
	dataline.RegisterDataLineRoutes(router, dataService)
	docIntelService := docintel.NewService(logger)
	docintel.RegisterDocIntelRoutes(router, docIntelService)
	taxPackService := taxpack.NewService(logger)
	taxpack.RegisterTaxPackRoutes(router, taxPackService)
	complianceOpsService := complianceops.NewService(logger)
	complianceops.RegisterComplianceOpsRoutes(router, complianceOpsService)

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
