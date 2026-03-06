package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/b11902156/rag-gateway/gateway/internal/audit"
	"github.com/b11902156/rag-gateway/gateway/internal/metrics"
	"github.com/b11902156/rag-gateway/gateway/internal/telemetry"
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

// Metrics records an http_requests_total Prometheus counter after each request.
// The "path" label uses the matched route pattern (e.g. "/api/v1/query"),
// and "status_class" is "2xx", "4xx", "5xx", etc.
func Metrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		status := c.Writer.Status()
		statusClass := strconv.Itoa(status/100) + "xx"
		metrics.RequestsTotal.WithLabelValues(c.FullPath(), statusClass).Inc()
	}
}

// OTelSpan starts a server-side OTel span for each request and ends it when
// the handler chain completes. The existing trace_id is attached as an attribute
// so logs and traces can be correlated. Span name is the matched route pattern
// (e.g. "rag.query") or the raw URL path for unmatched routes.
func OTelSpan() gin.HandlerFunc {
	tracer := telemetry.Tracer()
	return func(c *gin.Context) {
		spanName := c.FullPath()
		if spanName == "" {
			spanName = c.Request.URL.Path
		}
		// Map API routes to semantic span names.
		switch spanName {
		case "/api/v1/query":
			spanName = "rag.query"
		case "/api/v1/compile":
			spanName = "rag.compile"
		}

		ctx, span := tracer.Start(c.Request.Context(), spanName,
			trace.WithSpanKind(trace.SpanKindServer),
		)
		span.SetAttributes(
			attribute.String("trace_id", c.GetString("trace_id")),
			attribute.String("http.method", c.Request.Method),
			attribute.String("http.route", c.FullPath()),
		)
		c.Request = c.Request.WithContext(ctx)

		c.Next()

		status := c.Writer.Status()
		span.SetAttributes(attribute.Int("http.status_code", status))
		if status >= http.StatusInternalServerError {
			span.SetStatus(otelcodes.Error, http.StatusText(status))
		}
		span.End()
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
