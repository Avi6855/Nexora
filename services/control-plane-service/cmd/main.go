package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/control-plane-service/internal/dataplatform"
	"github.com/nexora/nexora/services/control-plane-service/internal/repository"
	"github.com/nexora/nexora/services/control-plane-service/internal/service"
	"github.com/nexora/nexora/services/control-plane-service/internal/transport"
	"github.com/nexora/nexora/shared/config"
	"github.com/nexora/nexora/shared/health"
)

func main() {
	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).With().Timestamp().Logger()
	cfg := config.LoadServiceConfig("control-plane-service", 8096, 8196)

	healthRepo := repository.NewInMemoryHealthRepository()
	healthService := service.NewHealthService(healthRepo, logger)
	dependencyService := service.NewDependencyService(healthRepo, healthService, logger)

	handlers := transport.NewHandlers(healthService, dependencyService, logger)
	router := mux.NewRouter()
	handlers.RegisterRoutes(router)
	dpSvc := dataplatform.NewService()
	dpHandlers := dataplatform.NewHandlers(dpSvc, logger)
	dpHandlers.RegisterRoutes(router)

	healthAddr := fmt.Sprintf(":%d", cfg.Service.Port+100)
	healthServer := health.NewHealthServer(healthAddr)

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
