package audit

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Logger writes audit events to both structured logs and Postgres.
type Logger struct {
	zap *zap.Logger
	db  *pgxpool.Pool
}

// New creates an audit Logger. db may be nil (falls back to log-only).
func New(z *zap.Logger, db *pgxpool.Pool) *Logger {
	return &Logger{zap: z, db: db}
}

// Log records an audit event. Details are written asynchronously to Postgres.
// Never blocks the request path.
func (l *Logger) Log(ctx context.Context, traceID, event, userID string, details map[string]any) {
	l.zap.Info("audit",
		zap.String("trace_id", traceID),
		zap.String("event", event),
		zap.String("user_id", userID),
	)
	if l.db != nil {
		go l.writeToDB(traceID, event, userID, details)
	}
}

func (l *Logger) writeToDB(traceID, event, userID string, details map[string]any) {
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		l.zap.Warn("audit: marshal details failed", zap.Error(err))
		return
	}
	var uid *string
	if userID != "" {
		uid = &userID
	}
	ctx := context.Background()
	_, err = l.db.Exec(ctx,
		`INSERT INTO audit_logs (trace_id, event, user_id, details) VALUES ($1, $2, $3, $4)`,
		traceID, event, uid, detailsJSON,
	)
	if err != nil {
		l.zap.Warn("audit: db write failed", zap.Error(err))
	}
}
