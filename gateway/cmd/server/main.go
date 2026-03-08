package main

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/b11902156/rag-gateway/gateway/config"
	"github.com/b11902156/rag-gateway/gateway/internal/adapter"
	"github.com/b11902156/rag-gateway/gateway/internal/adapterstore"
	"github.com/b11902156/rag-gateway/gateway/internal/audit"
	"github.com/b11902156/rag-gateway/gateway/internal/auth"
	"github.com/b11902156/rag-gateway/gateway/internal/cache"
	"github.com/b11902156/rag-gateway/gateway/internal/db"
	"github.com/b11902156/rag-gateway/gateway/internal/handler"
	"github.com/b11902156/rag-gateway/gateway/internal/logging"
	"github.com/b11902156/rag-gateway/gateway/internal/loramanager"
	"github.com/b11902156/rag-gateway/gateway/internal/middleware"
	"github.com/b11902156/rag-gateway/gateway/internal/policy"
	"github.com/b11902156/rag-gateway/gateway/internal/proxy"
	"github.com/b11902156/rag-gateway/gateway/internal/ratelimit"
	"github.com/b11902156/rag-gateway/gateway/internal/readiness"
	"github.com/b11902156/rag-gateway/gateway/internal/retrieval"
	"github.com/b11902156/rag-gateway/gateway/internal/telemetry"
)

func main() {
	cfg := config.Load()

	logger := logging.New()
	defer logger.Sync() //nolint:errcheck

	// Postgres (non-fatal: gateway degrades gracefully without DB).
	ctx := context.Background()

	// OpenTelemetry tracing (non-fatal: gateway works without a collector).
	otelShutdown, err := telemetry.Setup(ctx, "rag-gateway", cfg.OTelEndpoint)
	if err != nil {
		logger.Warn("telemetry setup failed, tracing disabled", zap.Error(err))
	} else {
		defer func() {
			if sErr := otelShutdown(ctx); sErr != nil {
				logger.Warn("telemetry shutdown error", zap.Error(sErr))
			}
		}()
	}
	var pgPool *pgxpool.Pool
	dbPool, err := db.New(ctx, cfg.PostgresHost, cfg.PostgresPort, cfg.PostgresUser, cfg.PostgresPass, cfg.PostgresDB)
	if err != nil {
		logger.Warn("postgres unavailable, audit writes disabled", zap.Error(err))
	} else {
		defer dbPool.Close()
		pgPool = dbPool.Pool
	}

	auditLogger := audit.New(logger, pgPool)

	// Adapter lineage store (non-fatal: degrades gracefully if DB is unavailable).
	adapterStore := adapterstore.New(pgPool, logger)

	// RSA public key for RS256 (optional; HS256 used when absent).
	rsaKey, err := auth.LoadRSAPublicKey(cfg.JWTPublicKeyPath)
	if err != nil {
		logger.Fatal("failed to load JWT public key", zap.Error(err))
	}

	// vLLM readiness probe — warmup goroutine starts immediately.
	probe := readiness.New(cfg.VLLMEndpoint, logger)

	// Retrieval gRPC client (non-fatal: RAG mode degrades gracefully if unavailable).
	rc, err := retrieval.New(cfg.RetrievalAddr, logger)
	if err != nil {
		logger.Warn("retrieval service unavailable, RAG mode disabled", zap.Error(err))
		rc = nil
	} else {
		defer rc.Close()
	}

	// Redis cache for retrieval results (non-fatal: gateway works without cache).
	var retriever proxy.Retriever
	if rc != nil {
		retriever = rc // default: uncached gRPC client
		if redisCache, redisErr := cache.New(cfg.RedisAddr); redisErr != nil {
			logger.Warn("redis unavailable, retrieval cache disabled", zap.Error(redisErr))
		} else {
			defer redisCache.Close()
			retriever = retrieval.NewCachedClient(rc, redisCache, cfg.RetrievalCacheTTL, logger)
			logger.Info("retrieval cache enabled",
				zap.String("addr", cfg.RedisAddr),
				zap.Duration("ttl", cfg.RetrievalCacheTTL),
			)
		}
	}

	// Policy engine (OPA) — non-fatal if OPA endpoint is empty.
	policyClient := policy.NewClient(cfg.OPAEndpoint)

	// Adapter Service gRPC client (non-fatal: compile mode degrades gracefully).
	ac, err := adapter.New(cfg.AdapterAddr, logger)
	if err != nil {
		logger.Warn("adapter service unavailable, compile mode disabled", zap.Error(err))
		ac = nil
	} else {
		defer ac.Close()
	}

	// vLLM LoRA session manager (always created; noop if adapter client is absent).
	loraMgr := loramanager.New(cfg.VLLMEndpoint, logger)

	// vLLM reverse proxy — attach retrieval, policy, adapter, and LoRA manager.
	vllmProxy := proxy.New(cfg.VLLMEndpoint, logger).WithPolicy(policyClient)
	if retriever != nil {
		vllmProxy.WithRetrieval(retriever)
	}
	// Wire the retrieval client as the indexer for the ingest endpoint.
	if rc != nil {
		vllmProxy.WithIndexer(rc)
	}
	if ac != nil {
		vllmProxy.WithAdapter(ac, cfg.AdapterStorePath)
		vllmProxy.WithLoraManager(loraMgr)
		vllmProxy.WithAdapterStore(adapterStore)
		// Hook loramanager TTL expiry into adapter lineage revocation.
		loraMgr.SetRevokeHook(adapterStore.Revoke)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.TraceID())
	r.Use(middleware.OTelSpan())
	r.Use(middleware.RequestLogger(logger))
	r.Use(middleware.AuditLog(auditLogger))
	r.Use(middleware.Metrics())

	h := handler.New(probe, vllmProxy)

	// Public endpoints (no auth)
	r.GET("/health", h.Health)
	r.GET("/ready", h.Ready)
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Authenticated endpoints — rate limited per IP.
	limiter := ratelimit.New(cfg.RateLimitRPM)
	api := r.Group("/api/v1")
	api.Use(limiter.Middleware())
	api.Use(auth.JWTMiddleware(cfg.JWTSecret, rsaKey))
	{
		api.POST("/query", h.Query)
		api.POST("/compile", h.Compile)
		api.POST("/ingest", h.Ingest)
	}

	logger.Info("starting gateway", zap.String("port", cfg.Port))
	if err := r.Run(":" + cfg.Port); err != nil {
		logger.Fatal("server failed", zap.Error(err))
	}
}
