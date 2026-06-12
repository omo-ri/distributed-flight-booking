package circuitbreaker

import (
	"testing"
	"time"
)

func TestNewStartsClosed(t *testing.T) {
	cb := New(3, time.Second, time.Minute)
	if err := cb.Allow(); err != nil {
		t.Fatalf("expected new breaker to allow requests, got %v", err)
	}
}

func TestStateString(t *testing.T) {
	cases := map[State]string{
		Closed:    "CLOSED",
		Open:      "OPEN",
		HalfOpen:  "HALF_OPEN",
		State(99): "UNKNOWN",
	}
	for state, want := range cases {
		if got := state.String(); got != want {
			t.Errorf("State(%d).String() = %q, want %q", state, got, want)
		}
	}
}

func TestTripsOpenAfterThreshold(t *testing.T) {
	cb := New(3, time.Second, time.Minute)

	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	if err := cb.Allow(); err != ErrCircuitOpen {
		t.Fatalf("expected ErrCircuitOpen after %d failures, got %v", 3, err)
	}
}

func TestStaysClosedBelowThreshold(t *testing.T) {
	cb := New(3, time.Second, time.Minute)

	cb.RecordFailure()
	cb.RecordFailure()

	if err := cb.Allow(); err != nil {
		t.Fatalf("expected breaker to stay closed below threshold, got %v", err)
	}
}

func TestHalfOpenRecoversOnSuccess(t *testing.T) {
	// short timeout so the breaker moves OPEN -> HALF_OPEN quickly
	cb := New(1, 10*time.Millisecond, time.Minute)

	cb.RecordFailure() // trips OPEN
	if err := cb.Allow(); err != ErrCircuitOpen {
		t.Fatalf("expected OPEN, got %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	// timeout elapsed: Allow probes (HALF_OPEN), then success closes it
	if err := cb.Allow(); err != nil {
		t.Fatalf("expected probe to be allowed after timeout, got %v", err)
	}
	cb.RecordSuccess()

	if err := cb.Allow(); err != nil {
		t.Fatalf("expected breaker closed after successful probe, got %v", err)
	}
}

func TestHalfOpenReopensOnFailure(t *testing.T) {
	cb := New(1, 10*time.Millisecond, time.Minute)

	cb.RecordFailure() // trips OPEN
	time.Sleep(20 * time.Millisecond)

	cb.Allow()         // moves to HALF_OPEN
	cb.RecordFailure() // probe fails -> back to OPEN

	if err := cb.Allow(); err != ErrCircuitOpen {
		t.Fatalf("expected OPEN after failed probe, got %v", err)
	}
}
