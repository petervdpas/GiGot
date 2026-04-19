package git

import (
	"encoding/json"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestAuditHeadEmpty returns no SHA and no error on a fresh repo that has
// never seen an audit event.
func TestAuditHeadEmpty(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.InitBare("repo"); err != nil {
		t.Fatalf("init: %v", err)
	}
	head, err := m.AuditHead("repo")
	if err != nil {
		t.Fatalf("AuditHead: %v", err)
	}
	if head != "" {
		t.Errorf("expected empty head on fresh repo, got %q", head)
	}
}

// TestAppendAuditFirstEntry bootstraps the ref with no parent and verifies
// the commit carries the GiGot Audit identity, zero parents, and the event
// JSON at event.json.
func TestAppendAuditFirstEntry(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.InitBare("repo"); err != nil {
		t.Fatalf("init: %v", err)
	}

	ev := AuditEvent{
		Type:  "repo_create",
		Actor: AuditActor{ID: "admin-1", Username: "alice", Provider: "session"},
		Notes: "initial repo",
	}
	sha, err := m.AppendAudit("repo", ev)
	if err != nil {
		t.Fatalf("AppendAudit: %v", err)
	}
	if sha == "" {
		t.Fatal("expected non-empty commit SHA")
	}

	head, err := m.AuditHead("repo")
	if err != nil {
		t.Fatalf("AuditHead: %v", err)
	}
	if head != sha {
		t.Errorf("audit head = %q, want %q", head, sha)
	}

	repoPath := m.RepoPath("repo")
	parents := runGit(t, repoPath, "rev-list", "--parents", "-n", "1", sha)
	if fields := strings.Fields(parents); len(fields) != 1 {
		t.Errorf("first audit commit should have no parents, got %q", parents)
	}

	author := runGit(t, repoPath, "show", "-s", "--format=%an <%ae>", sha)
	if got, want := strings.TrimSpace(author), auditAuthorName+" <"+auditAuthorEmail+">"; got != want {
		t.Errorf("author = %q, want %q", got, want)
	}

	raw := runGit(t, repoPath, "show", sha+":"+auditEventPath)
	var got AuditEvent
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if got.Type != ev.Type || got.Actor != ev.Actor || got.Notes != ev.Notes {
		t.Errorf("event roundtrip mismatch: got %+v, want %+v", got, ev)
	}
	if got.Time.IsZero() {
		t.Error("AppendAudit should stamp Time when caller leaves it zero")
	}
}

// TestAppendAuditChain verifies the second entry points at the first via
// its parent link — the "blockchain" property the design rests on.
func TestAppendAuditChain(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.InitBare("repo"); err != nil {
		t.Fatalf("init: %v", err)
	}

	first, err := m.AppendAudit("repo", AuditEvent{Type: "push_received", SHA: "deadbeef"})
	if err != nil {
		t.Fatalf("first AppendAudit: %v", err)
	}
	second, err := m.AppendAudit("repo", AuditEvent{Type: "push_received", SHA: "cafef00d"})
	if err != nil {
		t.Fatalf("second AppendAudit: %v", err)
	}
	if first == second {
		t.Fatal("two audit commits should have distinct SHAs")
	}

	repoPath := m.RepoPath("repo")
	parents := strings.Fields(runGit(t, repoPath, "rev-list", "--parents", "-n", "1", second))
	if len(parents) != 2 || parents[1] != first {
		t.Errorf("second commit parent = %v, want [%s %s]", parents, second, first)
	}

	count := strings.TrimSpace(runGit(t, repoPath, "rev-list", "--count", AuditRef))
	if count != "2" {
		t.Errorf("rev-list count on %s = %q, want 2", AuditRef, count)
	}
}

// TestAppendAuditRequiresType rejects events with no type so callers can't
// silently write noise into the chain.
func TestAppendAuditRequiresType(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.InitBare("repo"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := m.AppendAudit("repo", AuditEvent{Notes: "oops"}); err == nil {
		t.Fatal("expected error for empty type")
	}
}

// TestAppendAuditConcurrent exercises the CAS retry loop: two goroutines
// appending in parallel must both land, chained, with no lost updates.
func TestAppendAuditConcurrent(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.InitBare("repo"); err != nil {
		t.Fatalf("init: %v", err)
	}
	// Seed so both goroutines always hit the CAS branch rather than racing
	// the create branch (which is also safe, but this keeps coverage honest).
	if _, err := m.AppendAudit("repo", AuditEvent{Type: "seed"}); err != nil {
		t.Fatalf("seed AppendAudit: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Tiny stagger so both goroutines are in-flight at once but not
			// perfectly lockstepped (flakier than a real race would be).
			time.Sleep(time.Duration(i) * time.Microsecond)
			if _, err := m.AppendAudit("repo", AuditEvent{Type: "push_received"}); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent AppendAudit: %v", err)
	}

	count := strings.TrimSpace(runGit(t, m.RepoPath("repo"), "rev-list", "--count", AuditRef))
	if count != "3" {
		t.Errorf("rev-list count on %s = %q, want 3 (seed + 2)", AuditRef, count)
	}
}

func runGit(t *testing.T, repoPath string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", repoPath}, args...)
	out, err := exec.Command("git", full...).Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}
