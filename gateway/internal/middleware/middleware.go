package middleware

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/b11902156/rag-gateway/gateway/internal/audit"
)

// TraceID assigns an immutable trace ID to each request.
func TraceID() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := c.GetHeader("X-Trace-ID")
		if traceID == "" {
			traceID = fmt.Sprintf("tr-%d", time.Now().UnixNano())
		}
		c.Set("trace_id", traceID)
		c.Header("X-Trace-ID", traceID)
		c.Next()
	}
}

// RequestLogger logs request details via zap.
func RequestLogger(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.Info("request",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
			zap.String("trace_id", c.GetString("trace_id")),
		)
	}
}

// AuditLog writes an audit row to Postgres after every request completes.
// Runs after TraceID and JWT middleware so trace_id and user_id are available.
func AuditLog(al *audit.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		traceID := c.GetString("trace_id")
		userID := c.GetString("user_id") // populated by JWT middleware (1b)
		details := map[string]any{
			"method": c.Request.Method,
			"path":   c.Request.URL.Path,
			"status": c.Writer.Status(),
			"ip":     c.ClientIP(),
		}
		al.Log(c.Request.Context(), traceID, "request", userID, details)
	}
}
