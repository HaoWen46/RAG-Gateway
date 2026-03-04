package proxy_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/b11902156/rag-gateway/gateway/internal/adapter"
	"github.com/b11902156/rag-gateway/gateway/internal/loramanager"
	pb "github.com/b11902156/rag-gateway/gateway/internal/pb/adapter/v1"
	"github.com/b11902156/rag-gateway/gateway/internal/proxy"
	"github.com/b11902156/rag-gateway/gateway/internal/retrieval"
)

var errRetrieval = errors.New("retrieval service unavailable")

// fakeAdapterServicer is a gRPC test double for the Adapter Service.
type fakeAdapterServicer struct {
	pb.UnimplementedAdapterServiceServer
	compileResp *pb.CompileResponse
	compileErr  error
	verifyResp  *pb.VerifyResponse
	verifyErr   error
	revokeResp  *pb.RevokeResponse
}

func (f *fakeAdapterServicer) Compile(_ context.Context, _ *pb.CompileRequest) (*pb.CompileResponse, error) {
	return f.compileResp, f.compileErr
}
func (f *fakeAdapterServicer) Verify(_ context.Context, _ *pb.VerifyRequest) (*pb.VerifyResponse, error) {
	return f.verifyResp, f.verifyErr
}
func (f *fakeAdapterServicer) Revoke(_ context.Context, _ *pb.RevokeRequest) (*pb.RevokeResponse, error) {
	if f.revokeResp != nil {
		return f.revokeResp, nil
	}
	return &pb.RevokeResponse{Success: true}, nil
}

// startFakeAdapterService starts an in-process gRPC server and returns an adapter.Client pointed at it.
func startFakeAdapterService(t *testing.T, svc *fakeAdapterServicer) *adapter.Client {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := grpc.NewServer()
	pb.RegisterAdapterServiceServer(s, svc)
	go s.Serve(lis)
	t.Cleanup(s.Stop)

	c, err := adapter.New(lis.Addr().String(), zap.NewNop())
	if err != nil {
		t.Fatalf("adapter.New: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

// fakeVLLMForCompile creates a test vLLM server accepting load/unload calls.
func fakeVLLMForCompile(loadStatus, unloadStatus int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/load_lora_adapter":
			w.WriteHeader(loadStatus)
		case "/v1/unload_lora_adapter":
			w.WriteHeader(unloadStatus)
		default:
			// Other paths (e.g. chat completions) → unused in compile tests
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func setupCompileRouter(vllmURL, adapterStorePath string, ac *adapter.Client, r proxy.Retriever) *gin.Engine {
	router := gin.New()
	p := proxy.New(vllmURL, zap.NewNop())
	lm := loramanager.New(vllmURL, zap.NewNop())
	p.WithRetrieval(r)
	p.WithAdapter(ac, adapterStorePath)
	p.WithLoraManager(lm)
	router.POST("/api/v1/compile", func(c *gin.Context) {
		c.Set("trace_id", "test-trace")
		c.Set("role", "analyst")
		p.Compile(c)
	})
	return router
}

func doCompile(router *gin.Engine, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/compile",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestCompile_Success(t *testing.T) {
	vllm := fakeVLLMForCompile(http.StatusOK, http.StatusOK)
	defer vllm.Close()

	svc := &fakeAdapterServicer{
		compileResp: &pb.CompileResponse{
			AdapterId: "adapter-abc",
			Signature: "sig123",
			ExpiresAt: time.Now().Add(5 * time.Minute).Unix(),
		},
		verifyResp: &pb.VerifyResponse{
			Valid: true,
			ProbeResults: []*pb.ProbeResult{
				{ProbeName: "instruction_override", Passed: true, Detail: "ok"},
			},
		},
	}
	ac := startFakeAdapterService(t, svc)

	stub := &stubRetriever{sections: []retrieval.Section{
		{DocumentID: "d1", SectionID: "d1::0", Content: "policy text", Score: 1.0, TrustTier: "public"},
	}}
	router := setupCompileRouter(vllm.URL, t.TempDir(), ac, stub)
	w := doCompile(router, `{"query":"what is the policy?","ttl_seconds":300}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["adapter_id"] != "adapter-abc" {
		t.Errorf("adapter_id: got %v, want adapter-abc", resp["adapter_id"])
	}
	if resp["model"] != "adapter-abc" {
		t.Errorf("model field should match adapter_id")
	}
}

func TestCompile_MissingQuery_Returns400(t *testing.T) {
	vllm := fakeVLLMForCompile(http.StatusOK, http.StatusOK)
	defer vllm.Close()

	ac := startFakeAdapterService(t, &fakeAdapterServicer{})
	router := setupCompileRouter(vllm.URL, t.TempDir(), ac, &stubRetriever{})
	w := doCompile(router, `{"ttl_seconds":300}`) // no query field

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCompile_NoSections_Returns422(t *testing.T) {
	vllm := fakeVLLMForCompile(http.StatusOK, http.StatusOK)
	defer vllm.Close()

	ac := startFakeAdapterService(t, &fakeAdapterServicer{})
	stub := &stubRetriever{sections: []retrieval.Section{}} // empty
	router := setupCompileRouter(vllm.URL, t.TempDir(), ac, stub)
	w := doCompile(router, `{"query":"policy?"}`)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCompile_RetrievalError_Returns503(t *testing.T) {
	vllm := fakeVLLMForCompile(http.StatusOK, http.StatusOK)
	defer vllm.Close()

	ac := startFakeAdapterService(t, &fakeAdapterServicer{})
	stub := &stubRetriever{err: errRetrieval}
	router := setupCompileRouter(vllm.URL, t.TempDir(), ac, stub)
	w := doCompile(router, `{"query":"policy?"}`)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCompile_CanaryProbeFail_Returns422(t *testing.T) {
	vllm := fakeVLLMForCompile(http.StatusOK, http.StatusOK)
	defer vllm.Close()

	svc := &fakeAdapterServicer{
		compileResp: &pb.CompileResponse{
			AdapterId: "bad-adapter",
			Signature: "sig",
			ExpiresAt: time.Now().Add(time.Minute).Unix(),
		},
		verifyResp: &pb.VerifyResponse{
			Valid: false,
			ProbeResults: []*pb.ProbeResult{
				{ProbeName: "instruction_override", Passed: false, Detail: "adapter complied with override"},
			},
		},
	}
	ac := startFakeAdapterService(t, svc)
	stub := &stubRetriever{sections: []retrieval.Section{
		{DocumentID: "d1", SectionID: "d1::0", Content: "text", Score: 1.0, TrustTier: "public"},
	}}
	router := setupCompileRouter(vllm.URL, t.TempDir(), ac, stub)
	w := doCompile(router, `{"query":"policy?"}`)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 (probe fail), got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "adapter_revoked") {
		t.Errorf("expected adapter_revoked in response: %s", w.Body.String())
	}
}

func TestCompile_vLLMLoadFail_Returns502(t *testing.T) {
	vllm := fakeVLLMForCompile(http.StatusInternalServerError, http.StatusOK)
	defer vllm.Close()

	svc := &fakeAdapterServicer{
		compileResp: &pb.CompileResponse{
			AdapterId: "adapter-xyz",
			Signature: "sig",
			ExpiresAt: time.Now().Add(time.Minute).Unix(),
		},
		verifyResp: &pb.VerifyResponse{Valid: true},
	}
	ac := startFakeAdapterService(t, svc)
	stub := &stubRetriever{sections: []retrieval.Section{
		{DocumentID: "d1", SectionID: "d1::0", Content: "text", Score: 1.0, TrustTier: "public"},
	}}
	router := setupCompileRouter(vllm.URL, t.TempDir(), ac, stub)
	w := doCompile(router, `{"query":"policy?"}`)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 (vLLM load fail), got %d: %s", w.Code, w.Body.String())
	}
}

func TestCompile_NotConfigured_Returns501(t *testing.T) {
	// Without WithAdapter, Compile returns 501.
	router := gin.New()
	p := proxy.New("http://localhost:8000", zap.NewNop())
	router.POST("/api/v1/compile", func(c *gin.Context) {
		c.Set("trace_id", "t")
		p.Compile(c)
	})
	w := doCompile(router, `{"query":"test"}`)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCompile_SecurityHeaders(t *testing.T) {
	vllm := fakeVLLMForCompile(http.StatusOK, http.StatusOK)
	defer vllm.Close()

	svc := &fakeAdapterServicer{
		compileResp: &pb.CompileResponse{
			AdapterId: "adapter-h",
			Signature: "sig",
			ExpiresAt: time.Now().Add(time.Minute).Unix(),
		},
		verifyResp: &pb.VerifyResponse{Valid: true},
	}
	ac := startFakeAdapterService(t, svc)
	stub := &stubRetriever{sections: []retrieval.Section{
		{DocumentID: "d1", SectionID: "d1::0", Content: "text", Score: 1.0, TrustTier: "public"},
	}}
	router := setupCompileRouter(vllm.URL, t.TempDir(), ac, stub)
	w := doCompile(router, `{"query":"test"}`)

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
