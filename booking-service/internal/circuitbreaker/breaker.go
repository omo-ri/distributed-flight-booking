package circuitbreaker

import (
	"errors"
	"log"
	"sync"
	"time"
)

type State int

const (
	Closed   State = iota // normal — requests pass through
	Open                  // tripped — requests fail immediately
	HalfOpen              // probing — one request allowed
)

func (s State) String() string {
	switch s {
	case Closed:
		return "CLOSED"
	case Open:
		return "OPEN"
	case HalfOpen:
		return "HALF_OPEN"
	default:
		return "UNKNOWN"
	}
}

var ErrCircuitOpen = errors.New("circuit breaker is OPEN")

type CircuitBreaker struct {
	mu             sync.Mutex
	state          State
	errorCount     int
	errorThreshold int
	timeout        time.Duration // how long to stay OPEN before switching to HALF_OPEN
	window         time.Duration // time window for counting errors
	lastFailure    time.Time
	windowStart    time.Time
}

func New(errorThreshold int, timeout, window time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:          Closed,
		errorThreshold: errorThreshold,
		timeout:        timeout,
		window:         window,
		windowStart:    time.Now(),
	}
}

// Allow checks if a request is allowed. Returns false when OPEN.
func (cb *CircuitBreaker) Allow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case Closed:
		return nil
	case Open:
		if time.Since(cb.lastFailure) > cb.timeout {
			cb.setState(HalfOpen)
			return nil // allow one probe request
		}
		return ErrCircuitOpen
	case HalfOpen:
		return nil // allow the probe request
	}
	return nil
}

// RecordSuccess records a successful call.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == HalfOpen {
		cb.setState(Closed)
		cb.errorCount = 0
		cb.windowStart = time.Now()
	}
}

// RecordFailure records a failed call.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case Closed:
		// Reset counter if window expired
		if time.Since(cb.windowStart) > cb.window {
			cb.errorCount = 0
			cb.windowStart = time.Now()
		}
		cb.errorCount++
		cb.lastFailure = time.Now()
		if cb.errorCount >= cb.errorThreshold {
			cb.setState(Open)
		}
	case HalfOpen:
		// Probe failed — go back to OPEN
		cb.lastFailure = time.Now()
		cb.setState(Open)
	}
}

func (cb *CircuitBreaker) setState(newState State) {
	if cb.state != newState {
		log.Printf("[CIRCUIT-BREAKER] %s → %s", cb.state, newState)
		cb.state = newState
	}
}
