package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type CassandraConfig struct {
	Hosts       []string
	Keyspace    string
	Consistency string
	Timeout     time.Duration
	Username    string
	Password    string
}

type KafkaConfig struct {
	Brokers  []string
	GroupID  string
	TopicPrefix string
}

type ServiceConfig struct {
	Name    string
	Port    int
	GRPCPort int
	Version string
}

type AuthConfig struct {
	JWTSecret        string
	AccessTokenTTL   time.Duration
	RefreshTokenTTL  time.Duration
	OTPTTL           time.Duration
	MaxLoginAttempts int
}

type AppConfig struct {
	Cassandra CassandraConfig
	Kafka     KafkaConfig
	Service   ServiceConfig
	Auth      AuthConfig
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}

func getEnvDuration(key string, defaultVal time.Duration) time.Duration {
	if val := os.Getenv(key); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			return d
		}
	}
	return defaultVal
}

func getEnvSlice(key string, defaultVal []string) []string {
	if val := os.Getenv(key); val != "" {
		return strings.Split(val, ",")
	}
	return defaultVal
}

func LoadConfig() *AppConfig {
	return &AppConfig{
		Cassandra: CassandraConfig{
			Hosts:       getEnvSlice("CASSANDRA_HOSTS", []string{"127.0.0.1:9042"}),
			Keyspace:    getEnv("CASSANDRA_KEYSPACE", "nexora"),
			Consistency: getEnv("CASSANDRA_CONSISTENCY", "QUORUM"),
			Timeout:     getEnvDuration("CASSANDRA_TIMEOUT", 5*time.Second),
			Username:    getEnv("CASSANDRA_USERNAME", ""),
			Password:    getEnv("CASSANDRA_PASSWORD", ""),
		},
		Kafka: KafkaConfig{
			Brokers:     getEnvSlice("KAFKA_BROKERS", []string{"127.0.0.1:9092"}),
			GroupID:     getEnv("KAFKA_GROUP_ID", "nexora-consumers"),
			TopicPrefix: getEnv("KAFKA_TOPIC_PREFIX", "nexora"),
		},
		Service: ServiceConfig{
			Name:     getEnv("SERVICE_NAME", "nexora"),
			Port:     getEnvInt("SERVICE_PORT", 8080),
			GRPCPort: getEnvInt("SERVICE_GRPC_PORT", 8180),
			Version:  getEnv("SERVICE_VERSION", "0.1.0"),
		},
		Auth: AuthConfig{
			JWTSecret:        getEnv("JWT_SECRET", "nexora-default-secret-change-in-production"),
			AccessTokenTTL:   getEnvDuration("ACCESS_TOKEN_TTL", 15*time.Minute),
			RefreshTokenTTL:  getEnvDuration("REFRESH_TOKEN_TTL", 7*24*time.Hour),
			OTPTTL:           getEnvDuration("OTP_TTL", 5*time.Minute),
			MaxLoginAttempts: getEnvInt("MAX_LOGIN_ATTEMPTS", 5),
		},
	}
}

func LoadServiceConfig(name string, port, grpcPort int) *AppConfig {
	cfg := LoadConfig()
	cfg.Service.Name = name
	cfg.Service.Port = port
	cfg.Service.GRPCPort = grpcPort
	return cfg
}
