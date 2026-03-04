package circuitbreaker_test

import (
	"errors"
	"testing"
	"time"

	"github.com/b11902156/rag-gateway/gateway/internal/circuitbreaker"
)

func TestStartsClosed(t *testing.T) {
	cb := circuitbreaker.New(5, 30*time.Second)
	if err := cb.Allow(); err != nil {
		t.Fatalf("expected CLOSED, got error: %v", err)
	}
	if cb.State() != "closed" {
		t.Fatalf("expected state closed, got %s", cb.State())
	}
}

func TestTripsAfterMaxFailures(t *testing.T) {
	cb := circuitbreaker.New(3, 30*time.Second)
	for i := 0; i < 3; i++ {
		cb.Failure()
	}
	if cb.State() != "open" {
		t.Fatalf("expected open after 3 failures, got %s", cb.State())
	}
	if err := cb.Allow(); !errors.Is(err, circuitbreaker.ErrOpen) {
		t.Fatalf("expected ErrOpen, got %v", err)
	}
}

func TestSuccessResetsClosed(t *testing.T) {
	cb := circuitbreaker.New(3, 30*time.Second)
	for i := 0; i < 2; i++ {
		cb.Failure()
	}
	cb.Success()
	if cb.State() != "closed" {
		t.Fatalf("expected closed after success, got %s", cb.State())
	}
	if err := cb.Allow(); err != nil {
		t.Fatalf("expected allow after success, got %v", err)
	}
}

func TestHalfOpenAfterResetWindow(t *testing.T) {
	cb := circuitbreaker.New(3, 10*time.Millisecond)
	for i := 0; i < 3; i++ {
		cb.Failure()
	}
	if cb.State() != "open" {
		t.Fatalf("expected open, got %s", cb.State())
	}

	time.Sleep(20 * time.Millisecond) // wait for reset window

	// First Allow() should succeed (probe through HALF_OPEN).
	if err := cb.Allow(); err != nil {
		t.Fatalf("expected Allow() to pass after reset window, got %v", err)
	}
	if cb.State() != "half_open" {
		t.Fatalf("expected half_open state, got %s", cb.State())
	}

	// Subsequent Allow() while half_open must be rejected.
	if err := cb.Allow(); !errors.Is(err, circuitbreaker.ErrOpen) {
		t.Fatalf("expected ErrOpen while half_open, got %v", err)
	}
}

func TestHalfOpenSuccessCloses(t *testing.T) {
	cb := circuitbreaker.New(3, 10*time.Millisecond)
	for i := 0; i < 3; i++ {
		cb.Failure()
	}
	time.Sleep(20 * time.Millisecond)
	cb.Allow() // transition to half_open
	cb.Success()
	if cb.State() != "closed" {
		t.Fatalf("expected closed after half_open success, got %s", cb.State())
	}
}

func TestHalfOpenFailureReopens(t *testing.T) {
	cb := circuitbreaker.New(3, 10*time.Millisecond)
	for i := 0; i < 3; i++ {
		cb.Failure()
	}
	time.Sleep(20 * time.Millisecond)
	cb.Allow() // transition to half_open
	cb.Failure()
	if cb.State() != "open" {
		t.Fatalf("expected open after half_open failure, got %s", cb.State())
	}
}
