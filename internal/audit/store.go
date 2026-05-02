// Package audit stores a server-wide append-only log of admin events
// that don't belong to any single repository. Per-repo events still
// ride on the existing refs/audit/main chain inside each repo (see
// internal/git/audit.go); this store carries the events that span
// repos or live above them — tag catalogue lifecycle, account-level
// tag assignments, anything else that the per-repo chain can't host.
//
// On-disk file is sealed to the server's own public key, same NaCl-box
// pattern as accounts.enc / credentials.enc / tokens.enc. Rewrapped
// by `-rotate-keys` via crypto.DefaultSealedFiles.
package audit

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/petervdpas/GiGot/internal/crypto"
)

// Actor identifies the authenticated principal that caused an event.
// Empty when the server itself originated the action (boot-time
// migrations, scheduled tasks, etc.). Mirrors git.AuditActor so
// callers can pass the same struct between the two audit surfaces
// without translation.
type Actor struct {
	ID       string `json:"id,omitempty"`
	Username string `json:"username,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// Event is one row in the system audit log. Payload is free-form
// JSON — each event type defines its own shape (see
// docs/design/tags.md §7.1 for the tag-related events).
type Event struct {
	Time    time.Time       `json:"time"`
	Type    string          `json:"type"`
	Actor   Actor           `json:"actor,omitzero"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// SystemLog is the append-only sealed event store.
type SystemLog struct {
	file *crypto.SealedFile

	mu     sync.RWMutex
	events []Event
}

// Open loads (or initialises) the system audit log at path, sealed to
// enc. A missing file is fine — the log starts empty and grows as
// events are appended.
func Open(path string, enc *crypto.Encryptor) (*SystemLog, error) {
	f, err := crypto.NewSealedFile(path, enc)
	if err != nil {
		return nil, fmt.Errorf("audit: %w", err)
	}
	s := &SystemLog{file: f}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *SystemLog) load() error {
	plain, err := s.file.Load()
	if err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	if plain == nil {
		return nil
	}
	var list []Event
	if err := json.Unmarshal(plain, &list); err != nil {
		return fmt.Errorf("audit: parse: %w", err)
	}
	s.events = list
	return nil
}

func (s *SystemLog) persist() error {
	plain, err := json.Marshal(s.events)
	if err != nil {
		return fmt.Errorf("audit: marshal: %w", err)
	}
	return s.file.Save(plain)
}

// Append writes one event to the log. Time is stamped now if the
// caller left it zero; Type is required. Returns the persisted event
// (with Time filled in) so callers can echo it to the admin UI
// without re-reading.
func (s *SystemLog) Append(e Event) (Event, error) {
	if e.Type == "" {
		return Event{}, fmt.Errorf("audit: event type is required")
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.events = append(s.events, e)
	if err := s.persist(); err != nil {
		// Roll back the in-memory append so a write failure doesn't
		// leave the log diverged from disk.
		s.events = s.events[:len(s.events)-1]
		return Event{}, err
	}
	return e, nil
}

// All returns a snapshot of every event, newest-first. The admin UI
// reads from this for the "system events" table.
func (s *SystemLog) All() []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Time.After(out[j].Time)
	})
	return out
}

// Count returns the number of stored events.
func (s *SystemLog) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.events)
}
