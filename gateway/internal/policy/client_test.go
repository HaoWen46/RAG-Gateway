package policy_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/b11902156/rag-gateway/gateway/internal/policy"
)

// opaServer creates a test OPA server that returns the given allow value.
func opaServer(allow bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"result": allow})
	}))
}

// opaServerStatus creates a test OPA server that returns the given HTTP status.
func opaServerStatus(status int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
}

func TestCheckRetrieval_Allow(t *testing.T) {
	srv := opaServer(true)
	defer srv.Close()

	c := policy.NewClient(srv.URL)
	ok, err := c.CheckRetrieval(context.Background(), "analyst", []string{"internal"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected allow=true")
	}
}

func TestCheckRetrieval_Deny(t *testing.T) {
	srv := opaServer(false)
	defer srv.Close()

	c := policy.NewClient(srv.URL)
	ok, err := c.CheckRetrieval(context.Background(), "viewer", []string{"confidential"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected allow=false")
	}
}

func TestCheckCompile_Allow(t *testing.T) {
	srv := opaServer(true)
	defer srv.Close()

	c := policy.NewClient(srv.URL)
	ok, err := c.CheckCompile(context.Background(), "admin", []string{"sec1", "sec2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected allow=true")
	}
}

func TestCheckOutput_Allow(t *testing.T) {
	srv := opaServer(true)
	defer srv.Close()

	c := policy.NewClient(srv.URL)
	ok, err := c.CheckOutput(context.Background(), "viewer", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected allow=true")
	}
}

func TestCheckRetrieval_OPAUnreachable_DegradeAllow(t *testing.T) {
	// Use a closed server so the request fails immediately.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // close before use

	c := policy.NewClient(srv.URL)
	ok, err := c.CheckRetrieval(context.Background(), "analyst", []string{"internal"})
	// Should degrade to allow=true even though error is returned.
	if !ok {
		t.Errorf("expected degrade to allow=true on unreachable OPA, got %v (err: %v)", ok, err)
	}
}

func TestCheckRetrieval_OPA404_DegradeAllow(t *testing.T) {
	srv := opaServerStatus(http.StatusNotFound)
	defer srv.Close()

	c := policy.NewClient(srv.URL)
	ok, err := c.CheckRetrieval(context.Background(), "analyst", []string{"public"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected allow=true when policy rule not found")
	}
}

func TestEmptyEndpoint_DegradeAllow(t *testing.T) {
	c := policy.NewClient("") // no OPA configured
	ok, err := c.CheckRetrieval(context.Background(), "viewer", []string{"public"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected allow=true for empty endpoint")
	}
}

func TestCheckRetrieval_SendsInput(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		json.NewEncoder(w).Encode(map[string]any{"result": true})
	}))
	defer srv.Close()

	c := policy.NewClient(srv.URL)
	c.CheckRetrieval(context.Background(), "analyst", []string{"internal", "public"})

	input, _ := received["input"].(map[string]any)
	if input == nil {
		t.Fatal("OPA input not sent")
	}
	if input["user_role"] != "analyst" {
		t.Errorf("user_role: got %v, want analyst", input["user_role"])
	}
}
