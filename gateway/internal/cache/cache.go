// Package cache provides a thin Redis wrapper for the retrieval cache.
package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client wraps a Redis connection for get/set operations.
type Client struct {
	rdb *redis.Client
}

// New connects to Redis at addr and verifies the connection with a ping.
// Returns an error if the ping fails, so the caller can degrade gracefully.
func New(addr string) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("cache: ping %s: %w", addr, err)
	}
	return &Client{rdb: rdb}, nil
}

// Get returns the raw bytes stored at key, or an error (including redis.Nil on miss).
func (c *Client) Get(ctx context.Context, key string) ([]byte, error) {
	return c.rdb.Get(ctx, key).Bytes()
}

// Set stores val at key with the given TTL. A TTL of 0 means no expiry.
func (c *Client) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	return c.rdb.Set(ctx, key, val, ttl).Err()
}

// Close releases the Redis connection.
func (c *Client) Close() error {
	return c.rdb.Close()
}
