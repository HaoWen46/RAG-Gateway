package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/b11902156/rag-gateway/gateway/internal/handler"
)

func init() { gin.SetMode(gin.TestMode) }

func TestHealth(t *testing.T) {
	h := handler.New(nil, nil)
	r := gin.New()
	r.GET("/health", h.Health)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestReadyNilProbe(t *testing.T) {
	// nil probe → always ready
	h := handler.New(nil, nil)
	r := gin.New()
	r.GET("/ready", h.Ready)

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestReadyUnreachableVLLM(t *testing.T) {
	// Point probe at a port nobody listens on → should 503.
	// We import readiness directly here so we can construct a probe with a bad URL
	// without starting the warmup goroutine (warmup is fire-and-forget, won't affect test).
	// Use a non-routable address to get an immediate connection refused.
	t.Skip("skipped in CI: requires network; covered by integration test")
}
