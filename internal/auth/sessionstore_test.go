package auth

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestSealedSessionStore_EmptyFileReturnsNil(t *testing.T) {
	enc := newTestEncryptor(t)
	path := filepath.Join(t.TempDir(), "sessions.enc")
	s, err := NewSealedSessionStore(path, enc)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := s.LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	if entries != nil {
		t.Fatalf("expected nil entries for missing file, got %d", len(entries))
	}
}

func TestSealedSessionStore_RoundTrip(t *testing.T) {
	enc := newTestEncryptor(t)
	path := filepath.Join(t.TempDir(), "sessions.enc")
	s, _ := NewSealedSessionStore(path, enc)

	in := []*Session{
		{ID: "s1", Username: "alice", ExpiresAt: time.Now().Add(time.Hour)},
		{ID: "s2", Username: "bob", ExpiresAt: time.Now().Add(time.Hour)},
	}
	if err := s.SaveSessions(in); err != nil {
		t.Fatal(err)
	}
	out, err := s.LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(out))
	}
}

func TestSealedSessionStore_RejectsWrongKey(t *testing.T) {
	enc := newTestEncryptor(t)
	path := filepath.Join(t.TempDir(), "sessions.enc")
	s, _ := NewSealedSessionStore(path, enc)
	_ = s.SaveSessions([]*Session{{ID: "s1", Username: "alice", ExpiresAt: time.Now().Add(time.Hour)}})

	other := newTestEncryptor(t)
	s2, _ := NewSealedSessionStore(path, other)
	if _, err := s2.LoadSessions(); err == nil {
		t.Fatal("expected decryption to fail with a different key")
	}
}

func TestSessionStrategy_PersistsAcrossRestart(t *testing.T) {
	enc := newTestEncryptor(t)
	path := filepath.Join(t.TempDir(), "sessions.enc")

	store1, _ := NewSealedSessionStore(path, enc)
	s1 := NewSessionStrategy(time.Hour)
	if err := s1.SetPersister(store1); err != nil {
		t.Fatal(err)
	}
	sess, err := s1.Create("alice")
	if err != nil {
		t.Fatal(err)
	}

	// Simulate restart: fresh strategy + store pointing at the same file.
	store2, _ := NewSealedSessionStore(path, enc)
	s2 := NewSessionStrategy(time.Hour)
	if err := s2.SetPersister(store2); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	id, err := s2.Authenticate(req)
	if err != nil {
		t.Fatalf("session lost across restart: %v", err)
	}
	if id.Username != "alice" {
		t.Fatalf("identity corrupted: %+v", id)
	}
}

func TestSessionStrategy_DestroySurvivesRestart(t *testing.T) {
	enc := newTestEncryptor(t)
	path := filepath.Join(t.TempDir(), "sessions.enc")

	store, _ := NewSealedSessionStore(path, enc)
	s := NewSessionStrategy(time.Hour)
	_ = s.SetPersister(store)
	sess, _ := s.Create("alice")
	if !s.Destroy(sess.ID) {
		t.Fatal("destroy returned false")
	}

	store2, _ := NewSealedSessionStore(path, enc)
	s2 := NewSessionStrategy(time.Hour)
	_ = s2.SetPersister(store2)
	if s2.Count() != 0 {
		t.Fatalf("destroyed session resurrected after restart: %d", s2.Count())
	}
}

func TestSessionStrategy_ExpiredSessionsDroppedOnLoad(t *testing.T) {
	enc := newTestEncryptor(t)
	path := filepath.Join(t.TempDir(), "sessions.enc")

	// Hand-write a sealed file containing one live + one expired session.
	writer, _ := NewSealedSessionStore(path, enc)
	_ = writer.SaveSessions([]*Session{
		{ID: "live", Username: "alice", ExpiresAt: time.Now().Add(time.Hour)},
		{ID: "dead", Username: "alice", ExpiresAt: time.Now().Add(-time.Hour)},
	})

	reader, _ := NewSealedSessionStore(path, enc)
	s := NewSessionStrategy(time.Hour)
	if err := s.SetPersister(reader); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 1 {
		t.Fatalf("expected 1 live session, got %d", s.Count())
	}

	// And the expired entry should be scrubbed from the file too, so a
	// second restart doesn't see it either.
	entries, _ := reader.LoadSessions()
	if len(entries) != 1 || entries[0].ID != "live" {
		t.Fatalf("expired entry not scrubbed from disk: %+v", entries)
	}
}

func TestSessionStrategy_SetPersisterNilRejected(t *testing.T) {
	s := NewSessionStrategy(time.Hour)
	if err := s.SetPersister(nil); err == nil {
		t.Fatal("expected nil persister to be rejected")
	}
}
