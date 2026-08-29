package service

import (
	"context"
	"runtime"
	"time"

	"github.com/rs/zerolog"
	"github.com/nexora/nexora/services/control-plane-service/internal/repository"
)

type HealthService struct {
	repo      repository.HealthRepository
	startTime time.Time
	logger    zerolog.Logger
}

func NewHealthService(repo repository.HealthRepository, logger zerolog.Logger) *HealthService {
	return &HealthService{
		repo:      repo,
		startTime: time.Now().UTC(),
		logger:    logger,
	}
}

type SystemHealthResponse struct {
	Status    string            `json:"status"`
	Services  map[string]string `json:"services"`
	Uptime    string            `json:"uptime"`
	GoVersion string            `json:"go_version"`
	NumCPU    int               `json:"num_cpu"`
}

func (s *HealthService) GetSystemHealth(ctx context.Context) (*SystemHealthResponse, error) {
	serviceHealth, err := s.repo.GetAllServiceHealth(ctx)
	if err != nil {
		return nil, err
	}

	services := make(map[string]string)
	for name, health := range serviceHealth {
		services[name] = string(health.Status)
	}

	services["control-plane-service"] = "up"

	return &SystemHealthResponse{
		Status:    "healthy",
		Services:  services,
		Uptime:    time.Since(s.startTime).String(),
		GoVersion: runtime.Version(),
		NumCPU:    runtime.NumCPU(),
	}, nil
}

func (s *HealthService) GetServiceStatus(ctx context.Context, serviceName string) (map[string]string, error) {
	health, err := s.repo.GetServiceHealth(ctx, serviceName)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"service": health.ServiceName,
		"status":  string(health.Status),
	}, nil
}
