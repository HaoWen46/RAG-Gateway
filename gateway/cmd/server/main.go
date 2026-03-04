package main

import (
	"context"
	"log"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/b11902156/rag-gateway/gateway/config"
	"github.com/b11902156/rag-gateway/gateway/internal/audit"
	"github.com/b11902156/rag-gateway/gateway/internal/auth"
	"github.com/b11902156/rag-gateway/gateway/internal/db"
	"github.com/b11902156/rag-gateway/gateway/internal/handler"
	"github.com/b11902156/rag-gateway/gateway/internal/middleware"
	"github.com/b11902156/rag-gateway/gateway/internal/proxy"
	"github.com/b11902156/rag-gateway/gateway/internal/ratelimit"
	"github.com/b11902156/rag-gateway/gateway/internal/readiness"
)

func main() {
	cfg := config.Load()

	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("failed to init logger: %v", err)
	}
	defer logger.Sync()

	// Postgres (non-fatal: gateway degrades gracefully without DB).
	ctx := context.Background()
	var pgPool *pgxpool.Pool
	dbPool, err := db.New(ctx, cfg.PostgresHost, cfg.PostgresPort, cfg.PostgresUser, cfg.PostgresPass, cfg.PostgresDB)
	if err != nil {
		logger.Warn("postgres unavailable, audit writes disabled", zap.Error(err))
	} else {
		defer dbPool.Close()
		pgPool = dbPool.Pool
	}

	auditLogger := audit.New(logger, pgPool)

	// RSA public key for RS256 (optional; HS256 used when absent).
	rsaKey, err := auth.LoadRSAPublicKey(cfg.JWTPublicKeyPath)
	if err != nil {
		log.Fatalf("failed to load JWT public key: %v", err)
	}

	// vLLM readiness probe — warmup goroutine starts immediately.
	probe := readiness.New(cfg.VLLMEndpoint, logger)

	// vLLM reverse proxy.
	vllmProxy := proxy.New(cfg.VLLMEndpoint, logger)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.TraceID())
	r.Use(middleware.RequestLogger(logger))
	r.Use(middleware.AuditLog(auditLogger))

	h := handler.New(probe, vllmProxy)

	// Public endpoints (no auth)
	r.GET("/health", h.Health)
	r.GET("/ready", h.Ready)

	// Authenticated endpoints — rate limited per IP.
	limiter := ratelimit.New(cfg.RateLimitRPM)
	api := r.Group("/api/v1")
	api.Use(limiter.Middleware())
	api.Use(auth.JWTMiddleware(cfg.JWTSecret, rsaKey))
	{
		api.POST("/query", h.Query)
		api.POST("/compile", h.Compile)
	}

	logger.Info("starting gateway", zap.String("port", cfg.Port))
	if err := r.Run(":" + cfg.Port); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
