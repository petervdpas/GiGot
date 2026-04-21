package git

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// readAuditEventAt reads refs/audit/main~n's event.json and returns it
// as a decoded AuditEvent — test helper so individual cases stay short.
func readAuditEventAt(t *testing.T, repoPath string, offset int) AuditEvent {
	t.Helper()
	ref := AuditRef
	if offset > 0 {
		ref = AuditRef + "~" + itoa(offset)
	}
	out, err := exec.Command("git", "-C", repoPath, "show", ref+":event.json").Output()
	if err != nil {
		t.Fatalf("git show %s:event.json: %v", ref, err)
	}
	var ev AuditEvent
	if err := json.Unmarshal(out, &ev); err != nil {
		t.Fatalf("unmarshal %s event: %v", ref, err)
	}
	return ev
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// TestSeedAuditFromHistoryEmptyRepo returns zero and leaves the audit
// ref empty. Nothing to seed, nothing to fail.
func TestSeedAuditFromHistoryEmptyRepo(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.InitBare("empty"); err != nil {
		t.Fatalf("init: %v", err)
	}
	n, err := m.SeedAuditFromHistory("empty")
	if err != nil {
		t.Fatalf("SeedAuditFromHistory: %v", err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
	head, _ := m.AuditHead("empty")
	if head != "" {
		t.Errorf("audit head = %q, want empty", head)
	}
}

// TestSeedAuditFromHistorySeedsOneEntryPerCommit walks a repo with
// three commits and verifies the audit ref receives exactly three
// entries of type=commit with matching SHAs, Provider="backfill", and
// oldest-first ordering (so the top of the chain is the newest commit).
func TestSeedAuditFromHistorySeedsOneEntryPerCommit(t *testing.T) {
	m := NewManager(t.TempDir())
	c1 := seedBareWithFile(t, m, "multi", "a.txt", "one")
	c2 := putFile(t, m, "multi", c1, "b.txt", "two")
	c3 := putFile(t, m, "multi", c2, "c.txt", "three")

	n, err := m.SeedAuditFromHistory("multi")
	if err != nil {
		t.Fatalf("SeedAuditFromHistory: %v", err)
	}
	if n != 3 {
		t.Fatalf("count = %d, want 3", n)
	}

	repoPath := m.RepoPath("multi")

	// Chain depth should be exactly 3 (no extra entries).
	out, err := exec.Command("git", "-C", repoPath, "rev-list", "--count", AuditRef).Output()
	if err != nil {
		t.Fatalf("rev-list: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "3" {
		t.Errorf("chain depth = %q, want 3", got)
	}

	// Oldest-first: HEAD (offset 0) = c3, ~1 = c2, ~2 = c1.
	top := readAuditEventAt(t, repoPath, 0)
	mid := readAuditEventAt(t, repoPath, 1)
	bot := readAuditEventAt(t, repoPath, 2)

	for i, pair := range []struct {
		ev  AuditEvent
		sha string
	}{{top, c3}, {mid, c2}, {bot, c1}} {
		if pair.ev.Type != "commit" {
			t.Errorf("entry[%d] type = %q, want commit", i, pair.ev.Type)
		}
		if pair.ev.SHA != pair.sha {
			t.Errorf("entry[%d] sha = %q, want %q", i, pair.ev.SHA, pair.sha)
		}
		if pair.ev.Actor.Provider != "backfill" {
			t.Errorf("entry[%d] actor.provider = %q, want backfill", i, pair.ev.Actor.Provider)
		}
		if pair.ev.Actor.Username == "" {
			t.Errorf("entry[%d] actor.username empty, want a name from git log", i)
		}
	}
}

// TestSeedAuditFromHistoryIsIdempotent — calling twice never writes a
// duplicate set of entries. The second call is a no-op because the
// audit ref is already non-empty.
func TestSeedAuditFromHistoryIsIdempotent(t *testing.T) {
	m := NewManager(t.TempDir())
	c1 := seedBareWithFile(t, m, "once", "a.txt", "one")
	_ = putFile(t, m, "once", c1, "b.txt", "two")

	n1, err := m.SeedAuditFromHistory("once")
	if err != nil {
		t.Fatalf("first seed: %v", err)
	}
	if n1 != 2 {
		t.Fatalf("first call count = %d, want 2", n1)
	}
	n2, err := m.SeedAuditFromHistory("once")
	if err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if n2 != 0 {
		t.Errorf("second call count = %d, want 0 (idempotent)", n2)
	}

	// Chain depth must still be 2 — duplicates would double it.
	repoPath := m.RepoPath("once")
	out, _ := exec.Command("git", "-C", repoPath, "rev-list", "--count", AuditRef).Output()
	if got := strings.TrimSpace(string(out)); got != "2" {
		t.Errorf("chain depth after idempotent re-seed = %q, want 2", got)
	}
}

// TestSeedAuditFromHistoryPreservesExistingAudit — if the audit ref
// already has entries (real-time events), back-fill is a no-op even
// when the main branch has more commits than audit has entries. We
// deliberately never reorder or rewrite an existing chain.
func TestSeedAuditFromHistoryPreservesExistingAudit(t *testing.T) {
	m := NewManager(t.TempDir())
	c1 := seedBareWithFile(t, m, "mixed", "a.txt", "one")

	// Pre-populate one real-time audit entry.
	if _, err := m.AppendAudit("mixed", AuditEvent{
		Type:  "repo_create",
		Actor: AuditActor{Username: "alice", Provider: "session"},
		Notes: "real-time entry",
	}); err != nil {
		t.Fatalf("AppendAudit: %v", err)
	}

	// Add two more commits after audit was populated.
	c2 := putFile(t, m, "mixed", c1, "b.txt", "two")
	_ = putFile(t, m, "mixed", c2, "c.txt", "three")

	n, err := m.SeedAuditFromHistory("mixed")
	if err != nil {
		t.Fatalf("SeedAuditFromHistory: %v", err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0 (existing audit → no-op)", n)
	}

	// Chain depth should still be exactly 1 (the original repo_create).
	repoPath := m.RepoPath("mixed")
	out, _ := exec.Command("git", "-C", repoPath, "rev-list", "--count", AuditRef).Output()
	if got := strings.TrimSpace(string(out)); got != "1" {
		t.Errorf("chain depth = %q, want 1 (backfill skipped)", got)
	}
	top := readAuditEventAt(t, repoPath, 0)
	if top.Type != "repo_create" {
		t.Errorf("top entry type = %q, want repo_create (unchanged)", top.Type)
	}
}

// TestBackfillAuditForAllSeedsEmptyReposSkipsPopulated — the sweep
// helper runs over every repo, seeds those with empty audit, skips
// those already populated.
func TestBackfillAuditForAllSeedsEmptyReposSkipsPopulated(t *testing.T) {
	m := NewManager(t.TempDir())

	// Repo A: committed, empty audit → should be seeded.
	seedBareWithFile(t, m, "a", "x.txt", "x")
	// Repo B: committed, audit already populated → should be skipped.
	seedBareWithFile(t, m, "b", "y.txt", "y")
	if _, err := m.AppendAudit("b", AuditEvent{Type: "repo_create"}); err != nil {
		t.Fatalf("prime b: %v", err)
	}
	// Repo C: empty repo → back-fill is a no-op.
	if err := m.InitBare("c"); err != nil {
		t.Fatalf("init c: %v", err)
	}

	if err := m.BackfillAuditForAll(); err != nil {
		t.Fatalf("BackfillAuditForAll: %v", err)
	}

	countRef := func(name string) string {
		out, _ := exec.Command("git", "-C", m.RepoPath(name),
			"rev-list", "--count", AuditRef).Output()
		return strings.TrimSpace(string(out))
	}
	if got := countRef("a"); got != "1" {
		t.Errorf("repo a audit depth = %q, want 1 (seeded)", got)
	}
	if got := countRef("b"); got != "1" {
		t.Errorf("repo b audit depth = %q, want 1 (skipped, unchanged)", got)
	}
	if got := countRef("c"); got != "" && got != "0" {
		t.Errorf("repo c audit depth = %q, want empty (no commits)", got)
	}
}

// putFile is a test helper that writes one file via the real WriteFile
// path. Returns the new HEAD SHA so tests can chain additional writes.
func putFile(t *testing.T, m *Manager, name, parent, path, content string) string {
	t.Helper()
	res, err := m.WriteFile(name, WriteOptions{
		ParentVersion:  parent,
		Path:           path,
		Content:        []byte(content),
		AuthorName:     testAuthor,
		AuthorEmail:    testEmail,
		CommitterName:  testCommitter,
		CommitterEmail: testCommEmail,
		Message:        "add " + path,
	})
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return res.Version
}
