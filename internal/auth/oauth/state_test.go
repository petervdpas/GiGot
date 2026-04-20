package oauth

import (
	"errors"
	"testing"
	"time"
)

func TestStateStore_PutTakeRoundTrip(t *testing.T) {
	s := NewStateStore(time.Minute)
	state, err := s.Put(StateEntry{Provider: "github", Nonce: "n", CodeVerifier: "v"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Take(state)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if got.Provider != "github" || got.Nonce != "n" || got.CodeVerifier != "v" {
		t.Fatalf("unexpected entry: %+v", got)
	}
}

func TestStateStore_TakeIsOneShot(t *testing.T) {
	s := NewStateStore(time.Minute)
	state, _ := s.Put(StateEntry{Provider: "github"})
	if _, err := s.Take(state); err != nil {
		t.Fatalf("first Take failed: %v", err)
	}
	if _, err := s.Take(state); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("second Take: want ErrStateNotFound, got %v", err)
	}
}

func TestStateStore_ExpiredEntryIsGone(t *testing.T) {
	s := NewStateStore(time.Minute)
	fixed := time.Now()
	s.now = func() time.Time { return fixed }

	state, _ := s.Put(StateEntry{Provider: "github"})

	// Jump forward past the TTL and try to take.
	s.now = func() time.Time { return fixed.Add(2 * time.Minute) }
	_, err := s.Take(state)
	if !errors.Is(err, ErrStateNotFound) && !errors.Is(err, ErrStateExpired) {
		t.Fatalf("expired Take: want ErrStateExpired or NotFound, got %v", err)
	}
}

func TestStateStore_SweepDropsStaleEntries(t *testing.T) {
	s := NewStateStore(time.Minute)
	fixed := time.Now()
	s.now = func() time.Time { return fixed }
	for range 5 {
		_, _ = s.Put(StateEntry{Provider: "github"})
	}
	if got := s.Len(); got != 5 {
		t.Fatalf("Len after Puts = %d, want 5", got)
	}

	// Jump past TTL. A Put on the advanced clock should sweep the
	// older entries even if nobody is reading.
	s.now = func() time.Time { return fixed.Add(2 * time.Minute) }
	_, _ = s.Put(StateEntry{Provider: "github"})
	if got := s.Len(); got != 1 {
		t.Fatalf("Len after sweep = %d, want 1 (only the fresh Put)", got)
	}
}

func TestStateStore_RejectsEmptyState(t *testing.T) {
	s := NewStateStore(time.Minute)
	if _, err := s.Take(""); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("empty Take: want ErrStateNotFound, got %v", err)
	}
}

func TestPKCEChallenge_Deterministic(t *testing.T) {
	a := PKCEChallenge("the-verifier")
	b := PKCEChallenge("the-verifier")
	if a != b {
		t.Fatalf("PKCE challenge is not deterministic: %s vs %s", a, b)
	}
	if a == PKCEChallenge("something-else") {
		t.Fatal("PKCE challenge collided across different verifiers")
	}
}
