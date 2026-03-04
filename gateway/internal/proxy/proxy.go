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
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/b11902156/rag-gateway/gateway/internal/circuitbreaker"
	"github.com/b11902156/rag-gateway/gateway/internal/firewall"
	"github.com/b11902156/rag-gateway/gateway/internal/policy"
	"github.com/b11902156/rag-gateway/gateway/internal/retrieval"
)

const (
	vllmPath      = "/v1/chat/completions"
	upstreamError = "upstream service error"
	ragTopK       = 5
)

// Retriever is the interface the proxy uses to fetch document sections.
// retrieval.Client satisfies this interface; tests may use a stub.
type Retriever interface {
	Retrieve(ctx context.Context, query, traceID string, topK int32) ([]retrieval.Section, error)
}

// Proxy forwards requests to vLLM and handles both buffered and SSE responses.
type Proxy struct {
	endpoint  string // e.g. "http://localhost:8000"
	client    *http.Client
	logger    *zap.Logger
	cb        *circuitbreaker.CB
	retrieval Retriever // optional; nil means direct proxy (no RAG)
	fw        *firewall.ContextFirewall
	policy    *policy.Client
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
		fw:     firewall.New(),
		policy: policy.NewClient(""), // disabled by default; set via WithPolicy
	}
}

// WithPolicy attaches an OPA policy client.
func (p *Proxy) WithPolicy(pc *policy.Client) *Proxy {
	p.policy = pc
	return p
}

// WithRetrieval attaches an optional retriever for RAG mode.
// Calling this enables cite-or-refuse: queries with no retrieved sections are rejected.
func (p *Proxy) WithRetrieval(r Retriever) *Proxy {
	p.retrieval = r
	return p
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

	// RAG mode: retrieve context and inject into messages before forwarding.
	if p.retrieval != nil {
		userRole := c.GetString("role") // set by JWT auth middleware
		augmented, ragErr := p.ragAugment(c.Request.Context(), payload, traceID, userRole)
		if ragErr != nil {
			if strings.HasPrefix(ragErr.Error(), "policy:") {
				c.JSON(http.StatusForbidden, gin.H{"error": "access denied by policy"})
			} else {
				// cite-or-refuse: no sections after firewall → reject the request.
				c.JSON(http.StatusUnprocessableEntity, gin.H{
					"error":         "no relevant content found for your query",
					"cite_required": true,
				})
			}
			return
		}
		payload = augmented
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

// ragAugment retrieves relevant sections and injects them as a system message.
// Returns an augmented payload or an error if no sections were found (cite-or-refuse).
// userRole is the JWT role claim used by the context firewall for trust-tier filtering.
func (p *Proxy) ragAugment(ctx context.Context, payload map[string]any, traceID, userRole string) (map[string]any, error) {
	query := extractLastUserQuery(payload)
	if query == "" {
		// No user message — skip retrieval (let vLLM handle as-is).
		return payload, nil
	}

	sections, err := p.retrieval.Retrieve(ctx, query, traceID, ragTopK)
	if err != nil {
		p.logger.Warn("proxy: retrieval failed, continuing without RAG",
			zap.String("trace_id", traceID), zap.Error(err))
		// Degrade gracefully: if retrieval service is down, skip cite-or-refuse.
		return payload, nil
	}

	// Policy gate: ask OPA whether this role may access the retrieved tiers.
	if allowed, pErr := p.policy.CheckRetrieval(ctx, userRole, collectTrustTiers(sections)); pErr == nil && !allowed {
		p.logger.Warn("proxy: policy denied retrieval",
			zap.String("trace_id", traceID), zap.String("role", userRole))
		return nil, fmt.Errorf("policy: retrieval denied")
	}

	// Context firewall: strip injection patterns and enforce trust-tier access.
	sections = p.fw.SanitizeSections(sections, userRole)

	if len(sections) == 0 {
		p.logger.Info("proxy: no sections after firewall, refusing",
			zap.String("trace_id", traceID), zap.String("query", query))
		return nil, fmt.Errorf("cite-or-refuse: no sections")
	}

	systemMsg := buildRAGSystemMessage(sections)
	p.logger.Info("proxy: RAG context injected",
		zap.String("trace_id", traceID),
		zap.Int("sections", len(sections)),
	)

	// Clone payload and prepend system message.
	augmented := shallowCopyMap(payload)
	messages := prependSystemMessage(augmented["messages"], systemMsg)
	augmented["messages"] = messages
	return augmented, nil
}

// extractLastUserQuery returns the content of the last user message in messages.
func extractLastUserQuery(payload map[string]any) string {
	messages, _ := payload["messages"].([]any)
	for i := len(messages) - 1; i >= 0; i-- {
		msg, ok := messages[i].(map[string]any)
		if !ok {
			continue
		}
		if role, _ := msg["role"].(string); role == "user" {
			content, _ := msg["content"].(string)
			return content
		}
	}
	return ""
}

// buildRAGSystemMessage formats retrieved sections into a system prompt.
func buildRAGSystemMessage(sections []retrieval.Section) string {
	var b strings.Builder
	b.WriteString("You are a helpful assistant operating in RAG mode. ")
	b.WriteString("Answer the user's question based ONLY on the following retrieved sections. ")
	b.WriteString("You MUST include citations in the format [doc:<document_id>, sec:<section_id>] for every factual claim. ")
	b.WriteString("If you cannot answer from the provided sections, respond with: \"I cannot answer this question based on the available information.\"\n\n")
	b.WriteString("Retrieved sections:\n")
	for i, s := range sections {
		fmt.Fprintf(&b, "\n[%d] (doc: %s, sec: %s, trust: %s, score: %.2f)\n%s\n",
			i+1, s.DocumentID, s.SectionID, s.TrustTier, s.Score, s.Content)
	}
	return b.String()
}

// prependSystemMessage inserts a system message at the start of the messages array.
func prependSystemMessage(messages any, content string) []any {
	existing, _ := messages.([]any)
	sysMsg := map[string]any{"role": "system", "content": content}
	result := make([]any, 0, len(existing)+1)
	result = append(result, sysMsg)
	result = append(result, existing...)
	return result
}

func shallowCopyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// collectTrustTiers returns the unique trust tiers present in sections.
func collectTrustTiers(sections []retrieval.Section) []string {
	seen := make(map[string]struct{}, len(sections))
	out := make([]string, 0, len(sections))
	for _, s := range sections {
		if _, ok := seen[s.TrustTier]; !ok {
			seen[s.TrustTier] = struct{}{}
			out = append(out, s.TrustTier)
		}
	}
	return out
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
