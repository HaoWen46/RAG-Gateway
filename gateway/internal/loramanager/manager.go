// Package loramanager manages vLLM LoRA adapter lifecycle for session-scoped compile mode.
// It calls vLLM's /v1/load_lora_adapter and /v1/unload_lora_adapter endpoints and
// auto-revokes adapters when their TTL expires.
package loramanager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

type session struct {
	adapterName  string
	expiresAt    time.Time
	cancelRevoke context.CancelFunc
}

// Manager handles vLLM LoRA adapter load/unload and per-session TTL enforcement.
type Manager struct {
	vllmEndpoint string
	client       *http.Client
	logger       *zap.Logger

	mu       sync.Mutex
	sessions map[string]*session // sessionID → active session
}

// New creates a Manager targeting the given vLLM base endpoint (e.g. "http://localhost:8000").
func New(vllmEndpoint string, logger *zap.Logger) *Manager {
	return &Manager{
		vllmEndpoint: vllmEndpoint,
		client:       &http.Client{Timeout: 10 * time.Second},
		logger:       logger,
		sessions:     make(map[string]*session),
	}
}

// Load calls vLLM /v1/load_lora_adapter and schedules an auto-unload goroutine at expiresAt.
// If the session already has an active adapter, the old one is replaced.
func (m *Manager) Load(sessionID, adapterName, adapterPath string, expiresAt time.Time) error {
	if err := m.vllmLoad(adapterName, adapterPath); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())

	m.mu.Lock()
	if old, ok := m.sessions[sessionID]; ok {
		old.cancelRevoke() // cancel the previous TTL goroutine
	}
	m.sessions[sessionID] = &session{
		adapterName:  adapterName,
		expiresAt:    expiresAt,
		cancelRevoke: cancel,
	}
	m.mu.Unlock()

	go func() {
		ttl := time.Until(expiresAt)
		if ttl <= 0 {
			ttl = 0
		}
		select {
		case <-time.After(ttl):
			m.logger.Info("loramanager: TTL expired, auto-unloading adapter",
				zap.String("session_id", sessionID),
				zap.String("adapter", adapterName),
			)
			if err := m.Unload(sessionID); err != nil {
				m.logger.Warn("loramanager: auto-unload failed",
					zap.String("session_id", sessionID), zap.Error(err))
			}
		case <-ctx.Done():
			// Cancelled by an explicit Unload call — nothing to do.
		}
	}()

	return nil
}

// Unload cancels the session's TTL goroutine and calls vLLM /v1/unload_lora_adapter.
// Returns an error if the session is not found.
func (m *Manager) Unload(sessionID string) error {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if ok {
		sess.cancelRevoke()
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("loramanager: no active session %s", sessionID)
	}
	return m.vllmUnload(sess.adapterName)
}

// AdapterName returns the vLLM adapter name for a session.
// The adapter name is passed as the "model" field in subsequent chat completions.
// Returns ("", false) if the session is not found or has expired.
func (m *Manager) AdapterName(sessionID string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[sessionID]
	if !ok || time.Now().After(sess.expiresAt) {
		return "", false
	}
	return sess.adapterName, true
}

// ActiveSessions returns a snapshot of active session IDs (for monitoring/metrics).
func (m *Manager) ActiveSessions() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	ids := make([]string, 0, len(m.sessions))
	for id, sess := range m.sessions {
		if !now.After(sess.expiresAt) {
			ids = append(ids, id)
		}
	}
	return ids
}

func (m *Manager) vllmLoad(adapterName, adapterPath string) error {
	body, _ := json.Marshal(map[string]string{
		"lora_name": adapterName,
		"lora_path": adapterPath,
	})
	resp, err := m.client.Post(
		m.vllmEndpoint+"/v1/load_lora_adapter",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("loramanager: load_lora_adapter: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("loramanager: load_lora_adapter returned %d", resp.StatusCode)
	}
	m.logger.Info("loramanager: adapter loaded", zap.String("adapter", adapterName))
	return nil
}

func (m *Manager) vllmUnload(adapterName string) error {
	body, _ := json.Marshal(map[string]string{"lora_name": adapterName})
	resp, err := m.client.Post(
		m.vllmEndpoint+"/v1/unload_lora_adapter",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("loramanager: unload_lora_adapter: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("loramanager: unload_lora_adapter returned %d", resp.StatusCode)
	}
	m.logger.Info("loramanager: adapter unloaded", zap.String("adapter", adapterName))
	return nil
}
