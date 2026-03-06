package retrieval

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// cacheStore is the subset of cache.Client used by CachedClient.
// Defined here so tests can inject a fake without importing the cache package.
type cacheStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
}

// innerRetriever is the subset of Client used by CachedClient.
// Allows tests to inject a stub gRPC client.
type innerRetriever interface {
	Retrieve(ctx context.Context, query, traceID string, topK int32) ([]Section, error)
}

// CachedClient wraps a Retriever with a Redis-backed cache.
// On a cache hit it returns the stored sections without making a gRPC call.
// On a cache miss it calls the inner retriever and writes the result back.
// Any Redis error is logged and silently ignored (fail-open: fall through to gRPC).
type CachedClient struct {
	inner  innerRetriever
	store  cacheStore
	ttl    time.Duration
	logger *zap.Logger
}

// NewCachedClient returns a CachedClient backed by store with the given TTL.
func NewCachedClient(inner innerRetriever, store cacheStore, ttl time.Duration, logger *zap.Logger) *CachedClient {
	return &CachedClient{inner: inner, store: store, ttl: ttl, logger: logger}
}

// Retrieve checks the cache before calling the inner gRPC client.
func (c *CachedClient) Retrieve(ctx context.Context, query, traceID string, topK int32) ([]Section, error) {
	key := cacheKey(query, topK)

	if data, err := c.store.Get(ctx, key); err == nil {
		var sections []Section
		if jsonErr := json.Unmarshal(data, &sections); jsonErr == nil {
			c.logger.Debug("retrieval: cache hit", zap.String("key", key), zap.String("trace_id", traceID))
			return sections, nil
		}
	}

	sections, err := c.inner.Retrieve(ctx, query, traceID, topK)
	if err != nil {
		return nil, err
	}

	if data, jsonErr := json.Marshal(sections); jsonErr == nil {
		if setErr := c.store.Set(ctx, key, data, c.ttl); setErr != nil {
			c.logger.Warn("retrieval: cache write failed", zap.Error(setErr), zap.String("trace_id", traceID))
		}
	}
	return sections, nil
}

// cacheKey returns the Redis key for a (query, topK) pair.
// Format: retrieval:v1:<sha256(query:topK)>
func cacheKey(query string, topK int32) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", query, topK)))
	return fmt.Sprintf("retrieval:v1:%x", h)
}
