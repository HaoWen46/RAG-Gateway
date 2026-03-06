package adapterstore_test

import (
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/b11902156/rag-gateway/gateway/internal/adapter"
	"github.com/b11902156/rag-gateway/gateway/internal/adapterstore"
)

// TestStore_NilPool verifies that Record and Revoke are no-ops when the DB pool is nil.
// This ensures compile mode degrades gracefully when Postgres is unavailable.
func TestStore_NilPool_Record(t *testing.T) {
	s := adapterstore.New(nil, zap.NewNop())
	probes := []adapter.ProbeResult{
		{ProbeName: "injection", Passed: true, Detail: "ok"},
	}
	// Must not panic; background goroutine is never started when db == nil.
	s.Record("adapter-1", "session-1", "sig-abc", []string{"sec1", "sec2"}, probes, time.Now().Add(5*time.Minute))
}

func TestStore_NilPool_Revoke(t *testing.T) {
	s := adapterstore.New(nil, zap.NewNop())
	// Must not panic.
	s.Revoke("adapter-1", "canary_probe_failure")
	s.Revoke("adapter-2", "ttl_expired")
	s.Revoke("adapter-3", "vllm_load_failure")
}
