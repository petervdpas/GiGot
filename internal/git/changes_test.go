package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// collectChanges turns a []ChangeEntry into a map keyed by path so tests
// don't depend on the order diff-tree happens to emit.
func collectChanges(entries []ChangeEntry) map[string]ChangeEntry {
	out := make(map[string]ChangeEntry, len(entries))
	for _, e := range entries {
		out[e.Path] = e
	}
	return out
}

func TestChangesAddedModifiedDeleted(t *testing.T) {
	m := NewManager(t.TempDir())
	from := seedBareWithFile(t, m, "ch", "a.txt", "A1\n")
	// Second seed: add b.txt, modify a.txt, then delete a.txt in a third
	// step so the combined since→HEAD diff is A(b), D(a) — exercises both
	// added and deleted against the same base.
	pushBareCommit(t, m, "ch", map[string]string{
		"a.txt": "A2\n",
		"b.txt": "B\n",
	}, "modify a, add b")

	// Delete a.txt in a follow-up commit.
	repoPath := m.RepoPath("ch")
	work := t.TempDir()
	exec.Command("git", "clone", repoPath, work).Run()
	exec.Command("git", "-C", work, "config", "user.email", testCommEmail).Run()
	exec.Command("git", "-C", work, "config", "user.name", testCommitter).Run()
	exec.Command("git", "-C", work, "rm", "a.txt").Run()
	exec.Command("git", "-C", work, "commit", "-m", "drop a").Run()
	exec.Command("git", "-C", work, "push", "origin", "master").Run()

	info, err := m.Changes("ch", from)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	if info.From != from {
		t.Errorf("From: want %s, got %s", from, info.From)
	}
	head, _ := m.Head("ch")
	if info.To != head.Version {
		t.Errorf("To: want %s, got %s", head.Version, info.To)
	}

	byPath := collectChanges(info.Changes)
	a, ok := byPath["a.txt"]
	if !ok || a.Op != ChangeDeleted {
		t.Errorf("a.txt should be deleted, got %+v", a)
	}
	if a.Blob == "" {
		t.Error("deleted entry should carry the pre-change blob SHA")
	}
	b, ok := byPath["b.txt"]
	if !ok || b.Op != ChangeAdded {
		t.Errorf("b.txt should be added, got %+v", b)
	}
	if b.Blob == "" {
		t.Error("added entry should carry the new blob SHA")
	}
}

func TestChangesModifiedOnly(t *testing.T) {
	m := NewManager(t.TempDir())
	from := seedBareWithFile(t, m, "mo", "a.txt", "v1\n")
	pushBareCommit(t, m, "mo", map[string]string{"a.txt": "v2\n"}, "bump a")

	info, err := m.Changes("mo", from)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	if len(info.Changes) != 1 {
		t.Fatalf("want 1 change, got %+v", info.Changes)
	}
	if info.Changes[0].Op != ChangeModified {
		t.Errorf("want modified, got %s", info.Changes[0].Op)
	}
}

func TestChangesNoOp(t *testing.T) {
	m := NewManager(t.TempDir())
	from := seedBareWithFile(t, m, "no", "a.txt", "A\n")

	info, err := m.Changes("no", from)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	if info.From != from || info.To != from {
		t.Errorf("no-op: From and To should both be %s, got %+v", from, info)
	}
	if len(info.Changes) != 0 {
		t.Errorf("no-op should return empty Changes, got %+v", info.Changes)
	}
}

func TestChangesNestedAndSpacedPaths(t *testing.T) {
	m := NewManager(t.TempDir())
	from := seedBareWithFile(t, m, "np", "README.md", "hi\n")
	pushBareCommit(t, m, "np", map[string]string{
		"docs/notes with spaces.md": "body\n",
		"deep/nested/leaf.txt":      "leaf\n",
	}, "add nested")

	info, err := m.Changes("np", from)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	byPath := collectChanges(info.Changes)
	for _, p := range []string{"docs/notes with spaces.md", "deep/nested/leaf.txt"} {
		e, ok := byPath[p]
		if !ok {
			t.Errorf("missing %q in changes: %+v", p, info.Changes)
			continue
		}
		if e.Op != ChangeAdded {
			t.Errorf("%s: want added, got %s", p, e.Op)
		}
	}
}

// TestChangesMixedAddedModifiedDeleted exercises all three ops in a single
// diff and cross-checks each entry's Blob against the blob git itself has
// at the relevant side of the diff. This is the test that catches a
// parser regression swapping old-sha for new-sha or vice versa — the
// lighter tests only check `Blob != ""`.
func TestChangesMixedAddedModifiedDeleted(t *testing.T) {
	m := NewManager(t.TempDir())
	from := seedBareWithFile(t, m, "mx", "a.txt", "A1\n")

	// Seed a second file in the same starting commit via pushBareCommit,
	// then rewind `from` to point at this baseline so the upcoming diff
	// sees a.txt-modified, b.txt-deleted, c.txt-added all at once.
	pushBareCommit(t, m, "mx", map[string]string{"b.txt": "B\n"}, "add b")
	baseline, _ := m.Head("mx")
	from = baseline.Version

	// Single commit that modifies a.txt, deletes b.txt, and adds c.txt.
	repoPath := m.RepoPath("mx")
	work := t.TempDir()
	run := func(args ...string) {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", args, err, out)
		}
	}
	run("clone", repoPath, work)
	run("-C", work, "config", "user.email", testCommEmail)
	run("-C", work, "config", "user.name", testCommitter)
	if err := os.WriteFile(work+"/a.txt", []byte("A2\n"), 0644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(work+"/c.txt", []byte("C\n"), 0644); err != nil {
		t.Fatalf("write c: %v", err)
	}
	run("-C", work, "rm", "b.txt")
	run("-C", work, "add", "a.txt", "c.txt")
	run("-C", work, "commit", "-m", "mix a/b/c")
	run("-C", work, "push", "origin", "master")

	info, err := m.Changes("mx", from)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	if len(info.Changes) != 3 {
		t.Fatalf("want 3 changes, got %d: %+v", len(info.Changes), info.Changes)
	}

	revBlob := func(rev, path string) string {
		t.Helper()
		out, err := exec.Command("git", "-C", repoPath, "rev-parse", rev+":"+path).Output()
		if err != nil {
			t.Fatalf("rev-parse %s:%s: %v", rev, path, err)
		}
		return strings.TrimSpace(string(out))
	}

	wantBlob := map[string]string{
		"a.txt": revBlob(info.To, "a.txt"),   // modified: new blob at HEAD
		"b.txt": revBlob(info.From, "b.txt"), // deleted: old blob at from
		"c.txt": revBlob(info.To, "c.txt"),   // added: new blob at HEAD
	}
	wantOp := map[string]string{
		"a.txt": ChangeModified,
		"b.txt": ChangeDeleted,
		"c.txt": ChangeAdded,
	}

	byPath := collectChanges(info.Changes)
	for path, op := range wantOp {
		entry, ok := byPath[path]
		if !ok {
			t.Errorf("missing %s in changes: %+v", path, info.Changes)
			continue
		}
		if entry.Op != op {
			t.Errorf("%s: op want %s, got %s", path, op, entry.Op)
		}
		if entry.Blob != wantBlob[path] {
			t.Errorf("%s: blob want %s, got %s", path, wantBlob[path], entry.Blob)
		}
	}
}

func TestChangesMissingRepo(t *testing.T) {
	m := NewManager(t.TempDir())
	_, err := m.Changes("ghost", "HEAD")
	if err == nil {
		t.Fatal("expected missing-repo error")
	}
	if err == ErrRepoEmpty || err == ErrVersionNotFound {
		t.Fatalf("missing-repo must not be a sentinel; got %v", err)
	}
}

func TestChangesEmptyRepo(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.InitBare("empty"); err != nil {
		t.Fatalf("init: %v", err)
	}
	_, err := m.Changes("empty", "HEAD")
	if err != ErrRepoEmpty {
		t.Fatalf("want ErrRepoEmpty, got %v", err)
	}
}

func TestChangesBadSince(t *testing.T) {
	m := NewManager(t.TempDir())
	seedBareWithFile(t, m, "bs", "a.txt", "A\n")
	_, err := m.Changes("bs", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err != ErrVersionNotFound {
		t.Fatalf("want ErrVersionNotFound, got %v", err)
	}
}

func TestChangesMissingSince(t *testing.T) {
	m := NewManager(t.TempDir())
	seedBareWithFile(t, m, "ms", "a.txt", "A\n")
	_, err := m.Changes("ms", "")
	if err == nil {
		t.Fatal("expected error on missing since")
	}
	if err == ErrRepoEmpty || err == ErrVersionNotFound || err == ErrStaleParent {
		t.Fatalf("missing-since must not be a sentinel; got %v", err)
	}
}

func TestChangesStaleSince(t *testing.T) {
	m := NewManager(t.TempDir())
	seedBareWithFile(t, m, "sp", "a.txt", "v1\n")

	// Plant an orphan commit into the object store that is not an ancestor
	// of HEAD. Same trick as TestWriteFileStaleParent.
	otherSrc := seedSourceRepo(t, filepath.Join(t.TempDir(), "other"))
	otherBare := NewManager(t.TempDir())
	if err := otherBare.CloneBare("other", otherSrc); err != nil {
		t.Fatalf("CloneBare other: %v", err)
	}
	otherHead, _ := otherBare.Head("other")

	repoPath := m.RepoPath("sp")
	if out, err := exec.Command("git", "-C", repoPath, "fetch",
		otherBare.RepoPath("other"), otherHead.Version).CombinedOutput(); err != nil {
		t.Fatalf("fetch: %s: %v", out, err)
	}

	_, err := m.Changes("sp", otherHead.Version)
	if err != ErrStaleParent {
		t.Fatalf("want ErrStaleParent, got %v", err)
	}
}
