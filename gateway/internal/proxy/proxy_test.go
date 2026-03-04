package proxy_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/b11902156/rag-gateway/gateway/internal/proxy"
)

func init() { gin.SetMode(gin.TestMode) }

// fakevLLM starts a test server simulating vLLM responses.
func fakevLLM(handler http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(handler)
}

func setupRouter(vllmURL string) *gin.Engine {
	r := gin.New()
	p := proxy.New(vllmURL, zap.NewNop())
	r.POST("/api/v1/query", func(c *gin.Context) {
		c.Set("trace_id", "test-trace")
		p.Query(c)
	})
	return r
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
