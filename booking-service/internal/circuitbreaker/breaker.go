package circuitbreaker

import (
	"context"
	"errors"
	"log/slog"
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

// State 返回当前状态，供 grpcclient 往汇总行挂 cb 字段。
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// setState 打的是组件状态变更，不属于任何一条请求，所以独立成行（CLAUDE.md § 4）。
// 熔断打开影响的是它之后的所有请求，埋进某一条请求的字段里就找不到了。
// 级别：翻开是「异常但已自动处理」→ Warn；恢复是状态变更 → Info。
func (cb *CircuitBreaker) setState(newState State) {
	if cb.state == newState {
		return
	}
	lvl := slog.LevelInfo
	if newState == Open {
		lvl = slog.LevelWarn
	}
	slog.Default().Log(context.Background(), lvl, "circuit breaker state changed",
		"from", cb.state.String(),
		"to", newState.String(),
		"error_count", cb.errorCount,
		"error_threshold", cb.errorThreshold,
	)
	cb.state = newState
}
