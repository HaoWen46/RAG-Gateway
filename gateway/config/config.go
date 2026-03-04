package config

import (
	"os"
	"strconv"
)

type Config struct {
	Port              string
	PostgresHost      string
	PostgresPort      string
	PostgresUser      string
	PostgresPass      string
	PostgresDB        string
	RedisAddr         string
	OPAEndpoint       string
	RetrievalAddr     string
	AdapterAddr       string // gRPC address of the Adapter Service
	AdapterStorePath  string // shared FS path where Adapter Service writes PEFT dirs
	JWTSecret         string
	JWTPublicKeyPath  string // path to RSA public key PEM (enables RS256)
	VLLMEndpoint      string
	RateLimitRPM      int    // per-IP requests per minute (default 60)
}

func Load() *Config {
	return &Config{
		Port:          envOrDefault("GATEWAY_PORT", "8080"),
		PostgresHost:  envOrDefault("POSTGRES_HOST", "localhost"),
		PostgresPort:  envOrDefault("POSTGRES_PORT", "5432"),
		PostgresUser:  envOrDefault("POSTGRES_USER", "raggateway"),
		PostgresPass:  envOrDefault("POSTGRES_PASSWORD", "changeme"),
		PostgresDB:    envOrDefault("POSTGRES_DB", "raggateway"),
		RedisAddr:     envOrDefault("REDIS_ADDR", "localhost:6379"),
		OPAEndpoint:      envOrDefault("OPA_ENDPOINT", "http://localhost:8181"),
		RetrievalAddr:    envOrDefault("RETRIEVAL_ADDR", "localhost:50051"),
		AdapterAddr:      envOrDefault("ADAPTER_ADDR", "localhost:50053"),
		AdapterStorePath: envOrDefault("ADAPTER_STORE_PATH", "/tmp/adapters"),
		JWTSecret:        envOrDefault("JWT_SECRET", "changeme"),
		JWTPublicKeyPath: envOrDefault("JWT_PUBLIC_KEY_PATH", ""),
		VLLMEndpoint:     envOrDefault("VLLM_ENDPOINT", "http://localhost:8000"),
		RateLimitRPM:     envOrDefaultInt("RATE_LIMIT_RPM", 60),
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envOrDefaultInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}
