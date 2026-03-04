// Package circuitbreaker implements a lockless 3-state circuit breaker.
//
// State machine:
//
//	CLOSED ──(5 consecutive failures)──► OPEN
//	OPEN   ──(30 s elapsed)────────────► HALF_OPEN
//	HALF_OPEN ──(success)──────────────► CLOSED
//	HALF_OPEN ──(failure)──────────────► OPEN
package circuitbreaker

import (
	"errors"
	"sync/atomic"
	"time"
)

// ErrOpen is returned when the circuit is OPEN and the call is fast-failed.
var ErrOpen = errors.New("circuit breaker open")

type state uint32

const (
	stateClosed   state = 0
	stateOpen     state = 1
	stateHalfOpen state = 2
)

// CB is a lock-free circuit breaker safe for concurrent use.
type CB struct {
	state       atomic.Uint32
	failures    atomic.Uint32
	openAt      atomic.Int64 // UnixNano when circuit tripped OPEN
	maxFailures uint32
	resetAfter  time.Duration
}

// New creates a circuit breaker.
//
//   - maxFailures: consecutive failures before tripping OPEN (default 5)
//   - resetAfter: how long to stay OPEN before moving to HALF_OPEN (default 30 s)
func New(maxFailures uint32, resetAfter time.Duration) *CB {
	if maxFailures == 0 {
		maxFailures = 5
	}
	if resetAfter == 0 {
		resetAfter = 30 * time.Second
	}
	return &CB{maxFailures: maxFailures, resetAfter: resetAfter}
}

// Allow reports whether the call should proceed.
// Returns ErrOpen if the circuit is OPEN and the reset window has not elapsed.
// When HALF_OPEN, exactly one caller is allowed through.
func (cb *CB) Allow() error {
	s := state(cb.state.Load())
	switch s {
	case stateClosed:
		return nil

	case stateOpen:
		openAt := time.Unix(0, cb.openAt.Load())
		if time.Since(openAt) < cb.resetAfter {
			return ErrOpen
		}
		// Transition to HALF_OPEN (CAS so only one goroutine wins).
		if cb.state.CompareAndSwap(uint32(stateOpen), uint32(stateHalfOpen)) {
			return nil // this caller is the probe
		}
		// Another goroutine beat us; re-check state.
		if state(cb.state.Load()) == stateHalfOpen {
			return ErrOpen // only one probe at a time
		}
		return nil

	case stateHalfOpen:
		// Reject all except the one probe that transitioned us here.
		return ErrOpen
	}
	return nil
}

// Success records a successful upstream call.
func (cb *CB) Success() {
	cb.failures.Store(0)
	cb.state.Store(uint32(stateClosed))
}

// Failure records a failed upstream call and trips the breaker if needed.
func (cb *CB) Failure() {
	f := cb.failures.Add(1)
	s := state(cb.state.Load())
	if s == stateHalfOpen || f >= cb.maxFailures {
		cb.openAt.Store(time.Now().UnixNano())
		cb.state.Store(uint32(stateOpen))
	}
}

// State returns a human-readable description of the current state (for metrics).
func (cb *CB) State() string {
	switch state(cb.state.Load()) {
	case stateClosed:
		return "closed"
	case stateOpen:
		return "open"
	case stateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}
