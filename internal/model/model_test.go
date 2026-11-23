package model

import "testing"

func TestTransitionRules(t *testing.T) {
	if err := Transition(StateStaged, StateSynced, "id"); err == nil {
		t.Fatalf("expected STAGED -> SYNCED to fail")
	}
	if err := Transition(StateCreatingMedia, StateSynced, ""); err == nil {
		t.Fatalf("expected CREATING_MEDIA -> SYNCED without media ID to fail")
	}
	if err := Transition(StateCreatingMedia, StateSynced, "media-1"); err != nil {
		t.Fatalf("expected CREATING_MEDIA -> SYNCED with ID to pass: %v", err)
	}
}

func TestBackoffBounds(t *testing.T) {
	if got := Backoff(1, 60000); got != 60000 {
		t.Fatalf("expected floor 60000, got %d", got)
	}
	if got := Backoff(99, 0); got > 3600000 {
		t.Fatalf("expected cap <= 3600000, got %d", got)
	}
}

// TestRetryHorizon pins the automatic retry window: MaxAutoAttempts-1 waits
// (Backoff(1)..Backoff(15)) must span about 3 h at minimum jitter to about 6.1 h at maximum.
func TestRetryHorizon(t *testing.T) {
	const minTotal = 3 * 3600 * 1000
	const maxTotal = 6*3600*1000 + 200*1000
	for range 2000 {
		total := 0
		for n := 1; n < MaxAutoAttempts; n++ {
			total += Backoff(n, 0)
		}
		if total < minTotal || total > maxTotal {
			t.Fatalf("retry horizon %d ms outside [%d, %d]", total, minTotal, maxTotal)
		}
	}
}
