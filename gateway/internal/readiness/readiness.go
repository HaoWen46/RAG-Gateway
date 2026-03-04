package readiness

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

const (
	warmupInterval = 5 * time.Second
	warmupTimeout  = 5 * time.Minute
)

// Probe tracks whether the upstream vLLM service is ready.
type Probe struct {
	ready       atomic.Bool
	vllmHealth  string // e.g. "http://localhost:8000/health"
	logger      *zap.Logger
}

// New creates a Probe and immediately starts the warmup goroutine.
// The goroutine polls vLLM /health every 5 s for up to 5 min, then gives up
// (gateway keeps running; /ready returns 503 until vLLM recovers).
func New(vllmEndpoint string, logger *zap.Logger) *Probe {
	p := &Probe{
		vllmHealth: vllmEndpoint + "/health",
		logger:     logger,
	}
	go p.warmup()
	return p
}

// IsReady reports whether vLLM is currently considered up.
func (p *Probe) IsReady() bool {
	return p.ready.Load()
}

// warmup polls vLLM until it responds 200 or the timeout elapses.
func (p *Probe) warmup() {
	deadline := time.Now().Add(warmupTimeout)
	for time.Now().Before(deadline) {
		if p.ping() {
			p.ready.Store(true)
			p.logger.Info("vLLM is ready")
			return
		}
		p.logger.Info("waiting for vLLM", zap.String("url", p.vllmHealth))
		time.Sleep(warmupInterval)
	}
	p.logger.Warn("vLLM did not become ready within warmup window", zap.Duration("timeout", warmupTimeout))
}

// ping performs a single GET against vLLM /health.
// It also re-checks liveness on every call so IsReady stays accurate
// after the warmup window.
func (p *Probe) ping() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.vllmHealth, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Check performs a live poll (used by /ready handler on each request).
func (p *Probe) Check() error {
	if p.ping() {
		p.ready.Store(true)
		return nil
	}
	p.ready.Store(false)
	return fmt.Errorf("vLLM unreachable at %s", p.vllmHealth)
}
