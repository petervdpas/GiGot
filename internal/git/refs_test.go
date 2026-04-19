package git

import (
	"reflect"
	"strings"
	"testing"
)

func TestDiffRefSnapshotsCoversCreateUpdateDelete(t *testing.T) {
	before := map[string]string{
		"refs/heads/main":   "aaaa",
		"refs/heads/stale":  "bbbb",
		"refs/tags/v1":      "cccc",
	}
	after := map[string]string{
		"refs/heads/main":   "dddd", // updated
		"refs/heads/new":    "eeee", // created
		"refs/tags/v1":      "cccc", // unchanged → omitted
	}
	got := DiffRefSnapshots(before, after)
	want := []RefChange{
		{Ref: "refs/heads/main", OldSHA: "aaaa", NewSHA: "dddd", Kind: RefUpdated},
		{Ref: "refs/heads/new", NewSHA: "eeee", Kind: RefCreated},
		{Ref: "refs/heads/stale", OldSHA: "bbbb", Kind: RefDeleted},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DiffRefSnapshots mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

// TestDiffRefSnapshotsNoChange exercises the no-op branch — a receive-pack
// that rejected every update (non-ff, pre-receive refusal) leaves both
// snapshots equal, so the audit path must emit nothing.
func TestDiffRefSnapshotsNoChange(t *testing.T) {
	snap := map[string]string{"refs/heads/main": "aaaa"}
	if got := DiffRefSnapshots(snap, snap); len(got) != 0 {
		t.Fatalf("expected no changes, got %#v", got)
	}
	if got := DiffRefSnapshots(map[string]string{}, map[string]string{}); len(got) != 0 {
		t.Fatalf("expected no changes on empty snapshots, got %#v", got)
	}
}

// TestRefSnapshotExcludesAuditRefs locks in the exclusion of refs/audit/*
// from the snapshot so the audit writer's own advance never registers as a
// pushed change.
func TestRefSnapshotExcludesAuditRefs(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.InitBare("snap"); err != nil {
		t.Fatalf("InitBare: %v", err)
	}
	// Seed an audit entry so refs/audit/main exists.
	if _, err := m.AppendAudit("snap", AuditEvent{Type: "seed"}); err != nil {
		t.Fatalf("AppendAudit: %v", err)
	}
	snap, err := m.RefSnapshot("snap")
	if err != nil {
		t.Fatalf("RefSnapshot: %v", err)
	}
	for ref := range snap {
		if strings.HasPrefix(ref, "refs/audit/") {
			t.Errorf("snapshot should not contain %q", ref)
		}
	}
}
