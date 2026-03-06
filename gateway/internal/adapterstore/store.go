// Package adapterstore persists adapter compile/probe/revoke events to the adapters table.
// All writes are asynchronous (goroutine) and non-fatal: DB errors are logged and silently
// ignored so compile mode keeps working when Postgres is unavailable.
package adapterstore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/b11902156/rag-gateway/gateway/internal/adapter"
)

// Store writes adapter lineage records to Postgres.
// A nil-pool Store is valid and behaves as a no-op (graceful degrade).
type Store struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

// New returns a Store backed by db. db may be nil (log-only mode).
func New(db *pgxpool.Pool, logger *zap.Logger) *Store {
	return &Store{db: db, logger: logger}
}

// Record inserts a newly activated adapter into the adapters table.
// It is non-blocking: the write happens in a background goroutine.
func (s *Store) Record(adapterID, sessionID, signature string, sectionIDs []string, probes []adapter.ProbeResult, expiresAt time.Time) {
	if s.db == nil {
		return
	}
	go s.record(adapterID, sessionID, signature, sectionIDs, probes, expiresAt)
}

func (s *Store) record(adapterID, sessionID, signature string, sectionIDs []string, probes []adapter.ProbeResult, expiresAt time.Time) {
	probesJSON, err := json.Marshal(probes)
	if err != nil {
		s.logger.Warn("adapterstore: marshal probes failed", zap.String("adapter_id", adapterID), zap.Error(err))
		return
	}
	_, err = s.db.Exec(context.Background(),
		`INSERT INTO adapters (adapter_id, session_id, signature, section_ids, probe_results, status, expires_at)
		 VALUES ($1, $2, $3, $4, $5, 'active', $6)
		 ON CONFLICT (adapter_id) DO NOTHING`,
		adapterID, sessionID, signature, sectionIDs, probesJSON, expiresAt,
	)
	if err != nil {
		s.logger.Warn("adapterstore: record failed", zap.String("adapter_id", adapterID), zap.Error(err))
	}
}

// Revoke marks an adapter as revoked or expired in the adapters table.
// reason "ttl_expired" sets status="expired"; all other reasons set status="revoked".
// Non-blocking: the write happens in a background goroutine.
func (s *Store) Revoke(adapterID, reason string) {
	if s.db == nil {
		return
	}
	go s.revoke(adapterID, reason)
}

func (s *Store) revoke(adapterID, reason string) {
	status := "revoked"
	if reason == "ttl_expired" {
		status = "expired"
	}
	_, err := s.db.Exec(context.Background(),
		`UPDATE adapters
		 SET status = $1, revoked_at = NOW(), revoke_reason = $2
		 WHERE adapter_id = $3`,
		status, reason, adapterID,
	)
	if err != nil {
		s.logger.Warn("adapterstore: revoke failed", zap.String("adapter_id", adapterID), zap.Error(err))
	}
}
