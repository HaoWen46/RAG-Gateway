package loramanager_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/b11902156/rag-gateway/gateway/internal/loramanager"
)

// fakeVLLM creates a test server that responds to load/unload requests.
// It records the last received body so callers can assert on it.
func fakeVLLM(t *testing.T, loadStatus, unloadStatus int) (*httptest.Server, *string) {
	t.Helper()
	last := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		b, _ := json.Marshal(body)
		*last = string(b)
		switch r.URL.Path {
		case "/v1/load_lora_adapter":
			w.WriteHeader(loadStatus)
		case "/v1/unload_lora_adapter":
			w.WriteHeader(unloadStatus)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return srv, last
}

func TestLoad_Success(t *testing.T) {
	srv, last := fakeVLLM(t, http.StatusOK, http.StatusOK)
	defer srv.Close()

	mgr := loramanager.New(srv.URL, zap.NewNop())
	expiresAt := time.Now().Add(5 * time.Minute)
	if err := mgr.Load("sess1", "adapter-abc", "/adapters/adapter-abc", expiresAt); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify vLLM received correct payload.
	var got map[string]string
	json.Unmarshal([]byte(*last), &got)
	if got["lora_name"] != "adapter-abc" {
		t.Errorf("lora_name: got %q, want adapter-abc", got["lora_name"])
	}
	if got["lora_path"] != "/adapters/adapter-abc" {
		t.Errorf("lora_path: got %q, want /adapters/adapter-abc", got["lora_path"])
	}
}

func TestLoad_vLLMError_ReturnsError(t *testing.T) {
	srv, _ := fakeVLLM(t, http.StatusInternalServerError, http.StatusOK)
	defer srv.Close()

	mgr := loramanager.New(srv.URL, zap.NewNop())
	err := mgr.Load("sess1", "adapter-abc", "/adapters/adapter-abc", time.Now().Add(time.Minute))
	if err == nil {
		t.Fatal("expected error when vLLM returns 500")
	}
}

func TestAdapterName_ReturnsName(t *testing.T) {
	srv, _ := fakeVLLM(t, http.StatusOK, http.StatusOK)
	defer srv.Close()

	mgr := loramanager.New(srv.URL, zap.NewNop())
	mgr.Load("sess1", "adapter-abc", "/adapters/adapter-abc", time.Now().Add(time.Minute))

	name, ok := mgr.AdapterName("sess1")
	if !ok {
		t.Fatal("expected session to be found")
	}
	if name != "adapter-abc" {
		t.Errorf("got %q, want adapter-abc", name)
	}
}

func TestAdapterName_UnknownSession_ReturnsFalse(t *testing.T) {
	mgr := loramanager.New("http://localhost:99999", zap.NewNop())
	_, ok := mgr.AdapterName("no-such-session")
	if ok {
		t.Error("expected false for unknown session")
	}
}

func TestUnload_Success(t *testing.T) {
	srv, last := fakeVLLM(t, http.StatusOK, http.StatusOK)
	defer srv.Close()

	mgr := loramanager.New(srv.URL, zap.NewNop())
	mgr.Load("sess1", "adapter-abc", "/adapters/adapter-abc", time.Now().Add(time.Minute))
	if err := mgr.Unload("sess1"); err != nil {
		t.Fatalf("Unload failed: %v", err)
	}

	var got map[string]string
	json.Unmarshal([]byte(*last), &got)
	if got["lora_name"] != "adapter-abc" {
		t.Errorf("unload lora_name: got %q, want adapter-abc", got["lora_name"])
	}

	// Session should be gone.
	_, ok := mgr.AdapterName("sess1")
	if ok {
		t.Error("session should be removed after unload")
	}
}

func TestUnload_UnknownSession_ReturnsError(t *testing.T) {
	mgr := loramanager.New("http://localhost:99999", zap.NewNop())
	err := mgr.Unload("no-such-session")
	if err == nil {
		t.Fatal("expected error for unknown session")
	}
}

func TestTTL_AutoUnload(t *testing.T) {
	unloaded := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/unload_lora_adapter" {
			unloaded <- struct{}{}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	mgr := loramanager.New(srv.URL, zap.NewNop())
	expiresAt := time.Now().Add(100 * time.Millisecond) // very short TTL
	mgr.Load("sess1", "adapter-abc", "/adapters/adapter-abc", expiresAt)

	select {
	case <-unloaded:
		// adapter was auto-unloaded as expected
	case <-time.After(2 * time.Second):
		t.Fatal("adapter was not auto-unloaded within 2 seconds")
	}
}

func TestActiveSessions(t *testing.T) {
	srv, _ := fakeVLLM(t, http.StatusOK, http.StatusOK)
	defer srv.Close()

	mgr := loramanager.New(srv.URL, zap.NewNop())
	mgr.Load("sess1", "adapter-1", "/adapters/adapter-1", time.Now().Add(time.Minute))
	mgr.Load("sess2", "adapter-2", "/adapters/adapter-2", time.Now().Add(time.Minute))

	sessions := mgr.ActiveSessions()
	if len(sessions) != 2 {
		t.Errorf("expected 2 active sessions, got %d", len(sessions))
	}
}
