package proxy_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/b11902156/rag-gateway/gateway/internal/proxy"
	"github.com/b11902156/rag-gateway/gateway/internal/retrieval"
)

func init() { gin.SetMode(gin.TestMode) }

// fakevLLM starts a test server simulating vLLM responses.
func fakevLLM(handler http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(handler)
}

func setupRouter(vllmURL string) *gin.Engine {
	return setupRouterWithRetriever(vllmURL, nil)
}

func setupRouterWithRetriever(vllmURL string, r proxy.Retriever) *gin.Engine {
	router := gin.New()
	p := proxy.New(vllmURL, zap.NewNop())
	if r != nil {
		p.WithRetrieval(r)
	}
	router.POST("/api/v1/query", func(c *gin.Context) {
		c.Set("trace_id", "test-trace")
		p.Query(c)
	})
	return router
}

// stubRetriever is a test double for the Retriever interface.
type stubRetriever struct {
	sections []retrieval.Section
	err      error
}

func (s *stubRetriever) Retrieve(_ context.Context, _, _ string, _ int32) ([]retrieval.Section, error) {
	return s.sections, s.err
}

func TestBufferedProxy(t *testing.T) {
	srv := fakevLLM(func(w http.ResponseWriter, r *http.Request) {
		// Verify stream_options NOT injected for non-streaming request.
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["stream_options"]; ok {
			t.Error("stream_options should not be present for non-streaming request")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"content":"hello"}}]}`))
	})
	defer srv.Close()

	r := setupRouter(srv.URL)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/query",
		strings.NewReader(`{"model":"qwen","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "hello") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestStreamOptionsInjected(t *testing.T) {
	srv := fakevLLM(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		opts, ok := body["stream_options"].(map[string]any)
		if !ok {
			t.Error("stream_options missing for streaming request")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if opts["include_usage"] != true {
			t.Error("include_usage not true")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"))
	})
	defer srv.Close()

	r := setupRouter(srv.URL)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/query",
		strings.NewReader(`{"model":"qwen","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpstream5xx(t *testing.T) {
	srv := fakevLLM(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`internal vllm error with sensitive details`))
	})
	defer srv.Close()

	r := setupRouter(srv.URL)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/query",
		strings.NewReader(`{"model":"qwen","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
	// Must not leak upstream details.
	if strings.Contains(w.Body.String(), "sensitive") || strings.Contains(w.Body.String(), "vllm") {
		t.Fatalf("upstream internals leaked: %s", w.Body.String())
	}
}

func TestCircuitBreakerTrips(t *testing.T) {
	// Server always returns 500 → should trip CB after 5 failures → 503 on 6th.
	srv := fakevLLM(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer srv.Close()

	r := setupRouter(srv.URL)
	do := func() int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/query",
			strings.NewReader(`{"model":"qwen","messages":[]}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	// First 5 calls should get 502 (upstream 5xx).
	for i := 0; i < 5; i++ {
		if code := do(); code != http.StatusBadGateway {
			t.Fatalf("call %d: expected 502, got %d", i+1, code)
		}
	}
	// 6th call: circuit is now OPEN → 503.
	if code := do(); code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 from open circuit, got %d", code)
	}
}

func TestCiteOrRefuse_NoSections(t *testing.T) {
	// When retrieval returns 0 sections, expect 422.
	srv := fakevLLM(func(w http.ResponseWriter, r *http.Request) {
		// Should never be called.
		t.Error("vLLM should not be called when retrieval returns no sections")
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	stub := &stubRetriever{sections: []retrieval.Section{}} // no sections
	r := setupRouterWithRetriever(srv.URL, stub)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/query",
		strings.NewReader(`{"model":"qwen","messages":[{"role":"user","content":"what is the policy?"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 (cite-or-refuse), got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "cite_required") {
		t.Fatalf("expected cite_required in body: %s", w.Body.String())
	}
}

func TestCiteOrRefuse_WithSections(t *testing.T) {
	// When retrieval returns sections, they should be injected as a system message.
	var receivedMessages []any
	srv := fakevLLM(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		receivedMessages, _ = body["messages"].([]any)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"answer [doc:d1, sec:d1::0]"}}]}`))
	})
	defer srv.Close()

	stub := &stubRetriever{sections: []retrieval.Section{
		{DocumentID: "d1", SectionID: "d1::0", Content: "## Policy\nAll access is logged.", Score: 1.0, TrustTier: "public"},
	}}
	r := setupRouterWithRetriever(srv.URL, stub)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/query",
		strings.NewReader(`{"model":"qwen","messages":[{"role":"user","content":"what is the policy?"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	// First message must be the injected system message.
	if len(receivedMessages) < 2 {
		t.Fatalf("expected at least 2 messages (system + user), got %d", len(receivedMessages))
	}
	first, _ := receivedMessages[0].(map[string]any)
	if first["role"] != "system" {
		t.Errorf("first message role: got %q, want \"system\"", first["role"])
	}
	sysContent, _ := first["content"].(string)
	if !strings.Contains(sysContent, "d1") {
		t.Errorf("system message missing document id: %s", sysContent)
	}
	if !strings.Contains(sysContent, "citation") {
		t.Errorf("system message missing citation instruction: %s", sysContent)
	}
}

func TestCiteOrRefuse_RetrievalError_Degrades(t *testing.T) {
	// When retrieval returns an error (service down), proxy degrades gracefully
	// and forwards the request to vLLM without RAG context.
	srv := fakevLLM(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"direct answer"}}]}`))
	})
	defer srv.Close()

	stub := &stubRetriever{err: errors.New("retrieval service unavailable")}
	r := setupRouterWithRetriever(srv.URL, stub)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/query",
		strings.NewReader(`{"model":"qwen","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should degrade to direct proxy, not 422.
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (graceful degrade), got %d: %s", w.Code, w.Body.String())
	}
}

func TestSecurityHeaders(t *testing.T) {
	srv := fakevLLM(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	})
	defer srv.Close()

	r := setupRouter(srv.URL)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/query",
		strings.NewReader(`{"model":"qwen","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"X-Xss-Protection":       "1; mode=block",
	} {
		if got := w.Header().Get(header); got != want {
			t.Errorf("header %s: got %q, want %q", header, got, want)
		}
	}
}

func TestOutputFilter_MissingCitation_RAGMode(t *testing.T) {
	// vLLM returns a response with NO citation → output filter should reject with 422.
	srv := fakevLLM(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Response has no [doc:X, sec:Y] citation.
		w.Write([]byte(`{"choices":[{"message":{"content":"I don't know."}}]}`))
	})
	defer srv.Close()

	stub := &stubRetriever{sections: []retrieval.Section{
		{DocumentID: "d1", SectionID: "d1::0", Content: "Policy text.", Score: 1.0, TrustTier: "public"},
	}}
	r := setupRouterWithRetriever(srv.URL, stub)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/query",
		strings.NewReader(`{"model":"qwen","messages":[{"role":"user","content":"what is the policy?"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 (missing citation), got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "response_missing_citation") {
		t.Fatalf("expected response_missing_citation in body: %s", w.Body.String())
	}
}

func TestOutputFilter_NonRAGMode_NoCitationCheck(t *testing.T) {
	// Without a retriever, no citation check is performed — any response passes.
	srv := fakevLLM(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"plain answer without citation"}}]}`))
	})
	defer srv.Close()

	r := setupRouter(srv.URL) // no retriever
	req := httptest.NewRequest(http.MethodPost, "/api/v1/query",
		strings.NewReader(`{"model":"qwen","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (no citation check in non-RAG mode), got %d: %s", w.Code, w.Body.String())
	}
}

func TestOutputFilter_CitationPresent_RAGMode(t *testing.T) {
	// RAG mode with a proper citation → 200.
	srv := fakevLLM(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"The policy states X [doc:d1, sec:d1::0]."}}]}`))
	})
	defer srv.Close()

	stub := &stubRetriever{sections: []retrieval.Section{
		{DocumentID: "d1", SectionID: "d1::0", Content: "Policy.", Score: 1.0, TrustTier: "public"},
	}}
	r := setupRouterWithRetriever(srv.URL, stub)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/query",
		strings.NewReader(`{"model":"qwen","messages":[{"role":"user","content":"what is the policy?"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (citation present), got %d: %s", w.Code, w.Body.String())
	}
}
