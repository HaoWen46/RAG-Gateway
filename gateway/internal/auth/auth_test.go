package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/b11902156/rag-gateway/gateway/internal/auth"
)

const testSecret = "test-secret-key"

func makeToken(secret, sub, role string, expOffset time.Duration) string {
	claims := jwt.MapClaims{
		"sub":  sub,
		"role": role,
		"exp":  time.Now().Add(expOffset).Unix(),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok, _ := t.SignedString([]byte(secret))
	return tok
}

func setupRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(auth.JWTMiddleware(testSecret, nil))
	r.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"user_id": c.GetString("user_id"),
			"role":    c.GetString("role"),
		})
	})
	return r
}

func TestValidToken(t *testing.T) {
	r := setupRouter()
	tok := makeToken(testSecret, "user-123", "analyst", time.Hour)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !contains(body, "user-123") || !contains(body, "analyst") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestExpiredToken(t *testing.T) {
	r := setupRouter()
	tok := makeToken(testSecret, "user-123", "analyst", -time.Hour)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestWrongSecret(t *testing.T) {
	r := setupRouter()
	tok := makeToken("wrong-secret", "user-123", "analyst", time.Hour)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestMissingAuthHeader(t *testing.T) {
	r := setupRouter()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestMissingSub(t *testing.T) {
	r := setupRouter()
	// Token without "sub" claim
	claims := jwt.MapClaims{
		"role": "analyst",
		"exp":  time.Now().Add(time.Hour).Unix(),
	}
	t2 := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok, _ := t2.SignedString([]byte(testSecret))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing sub, got %d", w.Code)
	}
}

func TestErrorNotLeaked(t *testing.T) {
	r := setupRouter()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer garbage.not.a.jwt")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	body := w.Body.String()
	// Must not leak any JWT internals
	for _, leaked := range []string{"signature", "token", "parse", "malformed", "expired", "invalid"} {
		if contains(body, leaked) {
			t.Fatalf("error message leaks internals (%q): %s", leaked, body)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
