package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/b11902156/rag-gateway/gateway/internal/proxy"
	"github.com/b11902156/rag-gateway/gateway/internal/readiness"
)

// Handler holds dependencies for all HTTP handlers.
type Handler struct {
	probe *readiness.Probe
	proxy *proxy.Proxy
}

// New creates a Handler. probe and px may be nil (useful in tests).
func New(probe *readiness.Probe, px *proxy.Proxy) *Handler {
	return &Handler{probe: probe, proxy: px}
}

// Health is the liveness endpoint. Always 200, no auth required.
func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Ready is the readiness endpoint. Returns 503 if vLLM is not reachable.
func (h *Handler) Ready(c *gin.Context) {
	if h.probe == nil {
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
		return
	}
	if err := h.probe.Check(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready", "reason": "upstream unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}

// Query forwards RAG query requests to vLLM via the proxy.
func (h *Handler) Query(c *gin.Context) {
	if h.proxy == nil {
		c.JSON(http.StatusOK, gin.H{"answer": "stub response", "trace_id": c.GetString("trace_id")})
		return
	}
	h.proxy.Query(c)
}

// Compile handles compile-to-LoRA requests.
func (h *Handler) Compile(c *gin.Context) {
	var req struct {
		Query      string `json:"query" binding:"required"`
		TTLSeconds int    `json:"ttl_seconds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// TODO: Policy check → Retrieval → Adapter compile → Verify → Load LoRA
	c.JSON(http.StatusOK, gin.H{
		"adapter_id": "stub",
		"trace_id":   c.GetString("trace_id"),
	})
}
