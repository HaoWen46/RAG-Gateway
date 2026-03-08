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
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"

	"github.com/b11902156/rag-gateway/gateway/internal/adapter"
	"github.com/b11902156/rag-gateway/gateway/internal/adapterstore"
	"github.com/b11902156/rag-gateway/gateway/internal/circuitbreaker"
	"github.com/b11902156/rag-gateway/gateway/internal/firewall"
	"github.com/b11902156/rag-gateway/gateway/internal/loramanager"
	"github.com/b11902156/rag-gateway/gateway/internal/metrics"
	"github.com/b11902156/rag-gateway/gateway/internal/policy"
	"github.com/b11902156/rag-gateway/gateway/internal/retrieval"
	"github.com/b11902156/rag-gateway/gateway/internal/telemetry"
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

// Indexer is the interface the proxy uses to index new documents.
// retrieval.Client satisfies this interface.
type Indexer interface {
	Index(ctx context.Context, documentID, content string, metadata map[string]string) error
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

	indexer Indexer // optional; nil means ingest endpoint returns 503

	// Compile-mode fields (optional; nil disables compile mode).
	adapterClient    *adapter.Client
	adapterStorePath string              // shared filesystem path where Adapter Service writes PEFT dirs
	lora             *loramanager.Manager
	adapterStore     *adapterstore.Store // lineage persistence (nil = noop)
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

// WithIndexer attaches an indexer so the ingest endpoint can store documents.
func (p *Proxy) WithIndexer(i Indexer) *Proxy {
	p.indexer = i
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
	var injectedSections []retrieval.Section
	if p.retrieval != nil {
		userRole := c.GetString("role") // set by JWT auth middleware
		augmented, secs, ragErr := p.ragAugment(c.Request.Context(), payload, traceID, userRole)
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
		injectedSections = secs
		payload = augmented
	}
	ragActive := len(injectedSections) > 0

	streaming, _ := payload["stream"].(bool)
	// Streaming bypasses the cite-or-refuse output filter — block it in RAG mode.
	if ragActive && streaming {
		c.JSON(http.StatusBadRequest, gin.H{"error": "streaming is not supported in RAG mode"})
		return
	}
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

	// Span: vllm.forward — wraps the upstream HTTP call.
	fwdCtx, vllmSpan := telemetry.Tracer().Start(c.Request.Context(), "vllm.forward")
	req = req.WithContext(fwdCtx)

	start := time.Now()
	resp, err := p.client.Do(req)
	if err != nil {
		vllmSpan.RecordError(err)
		vllmSpan.SetStatus(otelcodes.Error, err.Error())
		vllmSpan.End()
		p.logger.Warn("proxy: upstream unreachable", zap.String("trace_id", traceID), zap.Error(err))
		p.cb.Failure()
		if errors.Is(err, context.Canceled) {
			return // client disconnected; no response needed
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": upstreamError})
		return
	}
	defer resp.Body.Close()
	vllmSpan.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))
	vllmSpan.End()

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
		p.bufferedResponse(c, resp, start, traceID, injectedSections)
	}
}

// ingestRequest is the parsed body for POST /api/v1/ingest.
type ingestRequest struct {
	DocumentID string            `json:"document_id"`
	Content    string            `json:"content"`
	TrustTier  string            `json:"trust_tier"`
	Metadata   map[string]string `json:"metadata"`
}

// Ingest is the handler for POST /api/v1/ingest.
// It indexes a document into the retrieval service so it is searchable by RAG queries.
// Requires admin or analyst role.
func (p *Proxy) Ingest(c *gin.Context) {
	setSecurityHeaders(c)

	role := c.GetString("role")
	if role != "admin" && role != "analyst" {
		c.JSON(http.StatusForbidden, gin.H{"error": "ingest requires admin or analyst role"})
		return
	}

	if p.indexer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "retrieval service unavailable"})
		return
	}

	var req ingestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.DocumentID == "" || req.Content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "document_id and content are required"})
		return
	}

	// Pass trust_tier as a metadata field so the retrieval service can surface it.
	meta := make(map[string]string, len(req.Metadata)+1)
	for k, v := range req.Metadata {
		meta[k] = v
	}
	if req.TrustTier != "" {
		meta["trust_tier"] = req.TrustTier
	}

	traceID := c.GetString("trace_id")
	if err := p.indexer.Index(c.Request.Context(), req.DocumentID, req.Content, meta); err != nil {
		p.logger.Warn("proxy: ingest failed",
			zap.String("trace_id", traceID),
			zap.String("document_id", req.DocumentID),
			zap.Error(err),
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "indexing failed"})
		return
	}

	metrics.DocumentsIndexed.Inc()
	p.logger.Info("proxy: document indexed",
		zap.String("trace_id", traceID),
		zap.String("document_id", req.DocumentID),
		zap.String("trust_tier", req.TrustTier),
	)
	c.JSON(http.StatusOK, gin.H{
		"document_id": req.DocumentID,
		"trust_tier":  req.TrustTier,
	})
}

// ragAugment retrieves relevant sections and injects them as a system message.
// Returns the augmented payload, the set of injected sections (nil = no RAG context),
// and an error if the request must be rejected (cite-or-refuse / policy).
// userRole is the JWT role claim used by the context firewall for trust-tier filtering.
func (p *Proxy) ragAugment(ctx context.Context, payload map[string]any, traceID, userRole string) (map[string]any, []retrieval.Section, error) {
	tracer := telemetry.Tracer()

	query := extractLastUserQuery(payload)
	if query == "" {
		// No user message — skip retrieval (let vLLM handle as-is).
		return payload, nil, nil
	}

	// Span: rag.retrieve
	_, retrieveSpan := tracer.Start(ctx, "rag.retrieve")
	sections, err := p.retrieval.Retrieve(ctx, query, traceID, ragTopK)
	if err != nil {
		retrieveSpan.RecordError(err)
		retrieveSpan.SetStatus(otelcodes.Error, err.Error())
		retrieveSpan.End()
		p.logger.Warn("proxy: retrieval failed, continuing without RAG",
			zap.String("trace_id", traceID), zap.Error(err))
		// Degrade gracefully: if retrieval service is down, skip cite-or-refuse.
		return payload, nil, nil
	}
	retrieveSpan.SetAttributes(attribute.Int("sections.count", len(sections)))
	retrieveSpan.End()

	// Span: rag.policy.check
	_, policySpan := tracer.Start(ctx, "rag.policy.check")
	allowed, pErr := p.policy.CheckRetrieval(ctx, userRole, collectTrustTiers(sections))
	if pErr == nil && !allowed {
		policySpan.SetStatus(otelcodes.Error, "retrieval denied")
		policySpan.End()
		p.logger.Warn("proxy: policy denied retrieval",
			zap.String("trace_id", traceID), zap.String("role", userRole))
		metrics.PolicyDenied.WithLabelValues("retrieval").Inc()
		return nil, nil, fmt.Errorf("policy: retrieval denied")
	}
	policySpan.End()

	// Span: rag.firewall — strip injection patterns and enforce trust-tier access.
	_, firewallSpan := tracer.Start(ctx, "rag.firewall")
	var fwStats firewall.SanitizeStats
	sections, fwStats = p.fw.SanitizeWithStats(sections, userRole)
	firewallSpan.SetAttributes(
		attribute.Int("sections.removed", fwStats.SectionsRemoved),
		attribute.Int("sentences.stripped", fwStats.SentencesStripped),
	)
	firewallSpan.End()
	if fwStats.SectionsRemoved > 0 {
		metrics.FirewallSectionsBlocked.Add(float64(fwStats.SectionsRemoved))
	}
	if fwStats.SentencesStripped > 0 {
		metrics.FirewallSentencesStripped.Add(float64(fwStats.SentencesStripped))
	}

	if len(sections) == 0 {
		p.logger.Info("proxy: no sections after firewall, refusing",
			zap.String("trace_id", traceID), zap.String("query", query))
		metrics.CiteOrRefuse.Inc()
		return nil, nil, fmt.Errorf("cite-or-refuse: no sections")
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
	return augmented, sections, nil
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

// bufferedResponse reads the full upstream body, applies output filters (citation
// presence + hallucination check + OPA policy), and writes the response to the client.
// retrievedSections is the set of sections injected into the RAG prompt; nil means
// no RAG context was active (non-RAG mode, output filters skipped).
func (p *Proxy) bufferedResponse(c *gin.Context, resp *http.Response, start time.Time, traceID string, retrievedSections []retrieval.Section) {
	ragActive := len(retrievedSections) > 0

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		p.logger.Warn("proxy: read upstream body failed", zap.String("trace_id", traceID), zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": upstreamError})
		return
	}

	hasCitation, hallucinated := verifyCitations(data, retrievedSections)

	// Hardcoded output filter: in RAG mode, the response MUST contain at least one citation.
	if ragActive && !hasCitation {
		p.logger.Warn("proxy: output filter rejected response (missing citation)",
			zap.String("trace_id", traceID))
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":                     "response does not contain required citations",
			"response_missing_citation": true,
		})
		return
	}

	// Citation verification: reject any citation to a section not in the retrieved set.
	if ragActive && hallucinated {
		p.logger.Warn("proxy: output filter rejected response (hallucinated citation)",
			zap.String("trace_id", traceID))
		metrics.HallucinatedCitations.Inc()
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":                 "response contains citations to content that was not retrieved",
			"hallucinated_citation": true,
		})
		return
	}

	// OPA output policy check (defense in depth; fail-open when OPA is unavailable).
	if ragActive {
		if allowed, _ := p.policy.CheckOutput(c.Request.Context(), ragActive, hasCitation); !allowed {
			p.logger.Warn("proxy: OPA output policy denied response",
				zap.String("trace_id", traceID))
			metrics.PolicyDenied.WithLabelValues("output").Inc()
			c.JSON(http.StatusForbidden, gin.H{"error": "response blocked by output policy"})
			return
		}
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

// citationParseRE captures (document_id, section_id) from [doc:<id>, sec:<id>] citations.
var citationParseRE = regexp.MustCompile(`\[doc:([^,\]]+),\s*sec:([^\]]+)\]`)

// sectionKey identifies a retrieved (document_id, section_id) pair.
type sectionKey struct{ docID, secID string }

// verifyCitations parses all [doc:<id>, sec:<id>] citations from the buffered
// LLM response and validates them against the set of actually-retrieved sections.
// Returns (hasCitations, hasHallucination):
//   - hasCitations: true if at least one citation was found
//   - hasHallucination: true if at least one citation refers to a section that
//     was NOT in the retrieved context (hallucinated citation)
func verifyCitations(data []byte, sections []retrieval.Section) (bool, bool) {
	matches := citationParseRE.FindAllSubmatch(data, -1)
	if len(matches) == 0 {
		return false, false
	}

	// Build a set of valid (docID, secID) pairs from what was actually retrieved.
	valid := make(map[sectionKey]struct{}, len(sections))
	for _, s := range sections {
		valid[sectionKey{strings.TrimSpace(s.DocumentID), strings.TrimSpace(s.SectionID)}] = struct{}{}
	}

	for _, m := range matches {
		key := sectionKey{
			docID: strings.TrimSpace(string(m[1])),
			secID: strings.TrimSpace(string(m[2])),
		}
		if _, ok := valid[key]; !ok {
			return true, true // at least one hallucinated citation
		}
	}
	return true, false
}

func setSecurityHeaders(c *gin.Context) {
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-Frame-Options", "DENY")
	c.Header("X-XSS-Protection", "1; mode=block")
}
