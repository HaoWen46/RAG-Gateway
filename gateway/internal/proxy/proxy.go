package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/b11902156/rag-gateway/gateway/internal/circuitbreaker"
)

const (
	vllmPath      = "/v1/chat/completions"
	upstreamError = "upstream service error"
)

// Proxy forwards requests to vLLM and handles both buffered and SSE responses.
type Proxy struct {
	endpoint string // e.g. "http://localhost:8000"
	client   *http.Client
	logger   *zap.Logger
	cb       *circuitbreaker.CB
}

// New creates a Proxy with a circuit breaker (5 failures → OPEN, 30 s reset).
func New(vllmEndpoint string, logger *zap.Logger) *Proxy {
	return &Proxy{
		endpoint: vllmEndpoint,
		client: &http.Client{
			Timeout: 0, // no global timeout; streaming responses can be long
		},
		logger: logger,
		cb:     circuitbreaker.New(5, 30*time.Second),
	}
}

// Query is the Gin handler for POST /api/v1/query.
func (p *Proxy) Query(c *gin.Context) {
	setSecurityHeaders(c)

	traceID := c.GetString("trace_id")

	// Circuit breaker guard.
	if err := p.cb.Allow(); err != nil {
		p.logger.Warn("proxy: circuit open, fast-fail", zap.String("trace_id", traceID))
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service temporarily unavailable"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 4<<20)) // 4 MB limit
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON"})
		return
	}

	streaming, _ := payload["stream"].(bool)
	if streaming {
		payload["stream_options"] = map[string]any{"include_usage": true}
	}

	forwardBody, err := json.Marshal(payload)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": upstreamError})
		return
	}

	url := p.endpoint + vllmPath
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, url, bytes.NewReader(forwardBody))
	if err != nil {
		p.logger.Error("proxy: build request failed", zap.String("trace_id", traceID), zap.Error(err))
		p.cb.Failure()
		c.JSON(http.StatusInternalServerError, gin.H{"error": upstreamError})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Trace-ID", traceID)

	start := time.Now()
	resp, err := p.client.Do(req)
	if err != nil {
		p.logger.Warn("proxy: upstream unreachable", zap.String("trace_id", traceID), zap.Error(err))
		p.cb.Failure()
		if errors.Is(err, context.Canceled) {
			return // client disconnected; no response needed
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": upstreamError})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		p.logger.Warn("proxy: upstream error", zap.String("trace_id", traceID), zap.Int("status", resp.StatusCode))
		p.cb.Failure()
		c.JSON(http.StatusBadGateway, gin.H{"error": upstreamError})
		return
	}

	// Any non-5xx counts as success for the circuit breaker.
	p.cb.Success()

	if resp.StatusCode >= 400 {
		c.JSON(resp.StatusCode, gin.H{"error": "bad request"})
		return
	}

	if streaming {
		p.streamResponse(c, resp, start, traceID)
	} else {
		p.bufferedResponse(c, resp, start, traceID)
	}
}

func (p *Proxy) bufferedResponse(c *gin.Context, resp *http.Response, start time.Time, traceID string) {
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		p.logger.Warn("proxy: read upstream body failed", zap.String("trace_id", traceID), zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": upstreamError})
		return
	}
	p.logger.Info("proxy: buffered complete",
		zap.String("trace_id", traceID),
		zap.Duration("duration", time.Since(start)),
	)
	c.Data(resp.StatusCode, "application/json", data)
}

func (p *Proxy) streamResponse(c *gin.Context, resp *http.Response, start time.Time, traceID string) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")

	flusher, canFlush := c.Writer.(http.Flusher)
	var ttftLogged bool

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if !ttftLogged && isSSEDataLine(line) {
			p.logger.Info("proxy: TTFT",
				zap.String("trace_id", traceID),
				zap.Duration("ttft", time.Since(start)),
			)
			ttftLogged = true
		}

		if _, err := fmt.Fprintf(c.Writer, "%s\n", line); err != nil {
			p.logger.Warn("proxy: client write failed", zap.String("trace_id", traceID), zap.Error(err))
			return
		}
		if canFlush {
			flusher.Flush()
		}
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
		p.logger.Warn("proxy: stream scan error", zap.String("trace_id", traceID), zap.Error(err))
	}

	p.logger.Info("proxy: stream complete",
		zap.String("trace_id", traceID),
		zap.Duration("duration", time.Since(start)),
	)
}

func isSSEDataLine(line string) bool {
	return len(line) >= 6 && line[:6] == "data: "
}

func setSecurityHeaders(c *gin.Context) {
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-Frame-Options", "DENY")
	c.Header("X-XSS-Protection", "1; mode=block")
}
