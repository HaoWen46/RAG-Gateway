package ratelimit_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/b11902156/rag-gateway/gateway/internal/ratelimit"
)

func init() { gin.SetMode(gin.TestMode) }

func setupRouter(rpm int) *gin.Engine {
	r := gin.New()
	l := ratelimit.New(rpm)
	r.Use(l.Middleware())
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func doRequest(r *gin.Engine) int {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:9999" // fixed IP
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestAllowsUnderLimit(t *testing.T) {
	r := setupRouter(60)
	// Should allow at least 1 request immediately (full burst on new bucket).
	if code := doRequest(r); code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
}

func TestDeniesOverLimit(t *testing.T) {
	// Set burst=3 (rpm=3) so we can exhaust quickly.
	r := setupRouter(3)
	allowed, denied := 0, 0
	for i := 0; i < 10; i++ {
		code := doRequest(r)
		if code == http.StatusOK {
			allowed++
		} else if code == http.StatusTooManyRequests {
			denied++
		}
	}
	if allowed == 0 {
		t.Fatal("expected at least some requests to be allowed")
	}
	if denied == 0 {
		t.Fatal("expected some requests to be rate limited")
	}
}

func TestRetryAfterHeader(t *testing.T) {
	r := setupRouter(1) // burst=1
	// Drain the bucket.
	doRequest(r)
	// Next request should be denied with Retry-After.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:9999"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header")
	}
}

func TestDifferentIPsIndependent(t *testing.T) {
	r := setupRouter(1) // burst=1 per IP
	for _, ip := range []string{"10.0.0.1:1", "10.0.0.2:1", "10.0.0.3:1"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("ip %s: expected 200, got %d", ip, w.Code)
		}
	}
}
