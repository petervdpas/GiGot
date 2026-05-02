package audit

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/petervdpas/GiGot/internal/crypto"
)

func newTestLog(t *testing.T) (*SystemLog, *crypto.Encryptor, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit_system.enc")
	priv, _, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := crypto.New(priv)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, enc)
	if err != nil {
		t.Fatal(err)
	}
	return s, enc, path
}

func TestAppend_StampsTimeAndPersists(t *testing.T) {
	s, _, _ := newTestLog(t)
	got, err := s.Append(Event{Type: "tag.created", Actor: Actor{Username: "peter"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Time.IsZero() {
		t.Fatal("Append did not stamp Time")
	}
	if s.Count() != 1 {
		t.Fatalf("count = %d, want 1", s.Count())
	}
}

func TestAppend_RequiresType(t *testing.T) {
	s, _, _ := newTestLog(t)
	if _, err := s.Append(Event{}); err == nil {
		t.Fatal("expected error for empty type")
	}
}

func TestAppend_PreservesCallerTime(t *testing.T) {
	s, _, _ := newTestLog(t)
	when := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	got, err := s.Append(Event{Type: "tag.created", Time: when})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Time.Equal(when) {
		t.Fatalf("Append overwrote caller-supplied Time: got %v, want %v", got.Time, when)
	}
}

func TestAll_ReturnsNewestFirst(t *testing.T) {
	s, _, _ := newTestLog(t)
	older := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	if _, err := s.Append(Event{Type: "tag.created", Time: older}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(Event{Type: "tag.renamed", Time: newer}); err != nil {
		t.Fatal(err)
	}
	got := s.All()
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Type != "tag.renamed" {
		t.Fatalf("newest first broken: got[0].Type = %q, want tag.renamed", got[0].Type)
	}
}

func TestRoundtrip_AcrossOpen(t *testing.T) {
	s, enc, path := newTestLog(t)
	payload, _ := json.Marshal(map[string]string{"name": "team:marketing"})
	if _, err := s.Append(Event{Type: "tag.created", Payload: payload}); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path, enc)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.Count() != 1 {
		t.Fatalf("count after reopen = %d, want 1", reopened.Count())
	}
	got := reopened.All()
	if got[0].Type != "tag.created" {
		t.Fatalf("type after reopen = %q, want tag.created", got[0].Type)
	}
	var p map[string]string
	if err := json.Unmarshal(got[0].Payload, &p); err != nil {
		t.Fatalf("payload after reopen unparseable: %v", err)
	}
	if p["name"] != "team:marketing" {
		t.Fatalf("payload after reopen = %v, want name=team:marketing", p)
	}
}

