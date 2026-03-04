package proxy

import (
	"net/http"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/b11902156/rag-gateway/gateway/internal/adapter"
	"github.com/b11902156/rag-gateway/gateway/internal/loramanager"
)

// WithAdapter attaches an Adapter Service client and the shared adapter filesystem path.
// adapterStorePath is the directory where the Adapter Service writes PEFT directories;
// the Gateway reads this path to tell vLLM where to find each adapter.
func (p *Proxy) WithAdapter(ac *adapter.Client, adapterStorePath string) *Proxy {
	p.adapterClient = ac
	p.adapterStorePath = adapterStorePath
	return p
}

// WithLoraManager attaches the vLLM LoRA session manager.
func (p *Proxy) WithLoraManager(lm *loramanager.Manager) *Proxy {
	p.lora = lm
	return p
}

// compileRequest is the JSON body for POST /api/v1/compile.
type compileRequest struct {
	Query      string `json:"query" binding:"required"`
	TTLSeconds int32  `json:"ttl_seconds"`
}

// Compile is the Gin handler for POST /api/v1/compile.
// It implements the Doc-to-LoRA pipeline:
//  1. Retrieve relevant document sections
//  2. Context firewall (trust-tier filter)
//  3. Policy gate: CheckCompile
//  4. Adapter Service: Compile (generates PEFT adapter)
//  5. Adapter Service: Verify (canary probes)
//  6. vLLM: load_lora_adapter
//  7. Schedule auto-revoke at TTL
func (p *Proxy) Compile(c *gin.Context) {
	setSecurityHeaders(c)

	traceID := c.GetString("trace_id")
	userRole := c.GetString("role")
	sessionID := traceID // trace_id scopes this compile session

	if p.adapterClient == nil || p.lora == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "compile mode not configured"})
		return
	}

	var req compileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: query is required"})
		return
	}
	if req.TTLSeconds <= 0 || req.TTLSeconds > 1800 {
		req.TTLSeconds = 300 // default 5 minutes; max 30 minutes
	}

	ctx := c.Request.Context()

	// Step 1: Retrieve relevant sections.
	if p.retrieval == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "retrieval service not configured"})
		return
	}
	sections, err := p.retrieval.Retrieve(ctx, req.Query, traceID, ragTopK)
	if err != nil {
		p.logger.Warn("compile: retrieval failed", zap.String("trace_id", traceID), zap.Error(err))
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "retrieval service unavailable"})
		return
	}
	if len(sections) == 0 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":         "no relevant content found for compilation",
			"cite_required": true,
		})
		return
	}

	// Step 2: Context firewall — enforce trust-tier access.
	sections = p.fw.SanitizeSections(sections, userRole)
	if len(sections) == 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "no accessible sections after trust-tier filter"})
		return
	}

	sectionIDs := make([]string, len(sections))
	for i, s := range sections {
		sectionIDs[i] = s.SectionID
	}

	// Step 3: Policy gate.
	if allowed, pErr := p.policy.CheckCompile(ctx, userRole, sectionIDs); pErr == nil && !allowed {
		p.logger.Warn("compile: policy denied",
			zap.String("trace_id", traceID), zap.String("role", userRole))
		c.JSON(http.StatusForbidden, gin.H{"error": "compile access denied by policy"})
		return
	}

	// Step 4: Compile adapter via Adapter Service.
	result, err := p.adapterClient.Compile(ctx, sessionID, traceID, sectionIDs, req.TTLSeconds)
	if err != nil {
		p.logger.Error("compile: adapter compile failed",
			zap.String("trace_id", traceID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "adapter compilation failed"})
		return
	}

	// Step 5: Verify integrity and run canary probes.
	valid, probes, err := p.adapterClient.Verify(ctx, result.AdapterID, result.Signature)
	if err != nil {
		p.logger.Error("compile: adapter verify failed",
			zap.String("trace_id", traceID), zap.String("adapter_id", result.AdapterID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "adapter verification error"})
		return
	}
	if !valid {
		p.logger.Warn("compile: adapter failed canary probes, revoking",
			zap.String("trace_id", traceID), zap.String("adapter_id", result.AdapterID))
		if rErr := p.adapterClient.Revoke(ctx, result.AdapterID); rErr != nil {
			p.logger.Error("compile: revoke after probe failure",
				zap.String("adapter_id", result.AdapterID), zap.Error(rErr))
		}
		failedProbes := make([]gin.H, 0, len(probes))
		for _, pr := range probes {
			if !pr.Passed {
				failedProbes = append(failedProbes, gin.H{
					"probe":  pr.ProbeName,
					"detail": pr.Detail,
				})
			}
		}
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":           "adapter failed safety verification",
			"failed_probes":   failedProbes,
			"adapter_revoked": true,
		})
		return
	}

	// Step 6: Load adapter into vLLM.
	adapterPath := filepath.Join(p.adapterStorePath, result.AdapterID)
	expiresAt := time.Unix(result.ExpiresAt, 0)
	if err := p.lora.Load(sessionID, result.AdapterID, adapterPath, expiresAt); err != nil {
		p.logger.Error("compile: vLLM load failed",
			zap.String("trace_id", traceID), zap.String("adapter_id", result.AdapterID), zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to activate adapter in vLLM"})
		return
	}

	p.logger.Info("compile: adapter active",
		zap.String("trace_id", traceID),
		zap.String("adapter_id", result.AdapterID),
		zap.Time("expires_at", expiresAt),
	)

	c.JSON(http.StatusOK, gin.H{
		"adapter_id": result.AdapterID,
		"model":      result.AdapterID, // use as "model" field in subsequent /api/v1/query calls
		"expires_at": result.ExpiresAt,
		"trace_id":   traceID,
	})
}
