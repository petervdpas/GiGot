package git

import (
	"encoding/base64"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func makeCommitOpts(parent string, changes []Change, message string) CommitOptions {
	return CommitOptions{
		ParentVersion:  parent,
		Changes:        changes,
		AuthorName:     testAuthor,
		AuthorEmail:    testEmail,
		CommitterName:  testCommitter,
		CommitterEmail: testCommEmail,
		Message:        message,
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func decodeB64(t *testing.T, s string) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return string(b)
}

func TestCommitRenameFastForward(t *testing.T) {
	m := NewManager(t.TempDir())
	parent := seedBareWithFile(t, m, "rn", "templates/a.yaml", "A\n")

	res, err := m.Commit("rn", makeCommitOpts(parent, []Change{
		{Op: OpDelete, Path: "templates/a.yaml"},
		{Op: OpPut, Path: "templates/c.yaml", Content: []byte("A\n")},
	}, "rename a -> c"))
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !res.FastForward {
		t.Error("expected fast-forward")
	}

	// Exactly one new commit.
	out, _ := exec.Command("git", "-C", m.RepoPath("rn"),
		"rev-list", "--count", parent+".."+res.Version).Output()
	if strings.TrimSpace(string(out)) != "1" {
		t.Errorf("expected exactly 1 new commit, got %q", out)
	}
	// Old path gone, new path present with same content.
	if _, err := m.File("rn", "", "templates/a.yaml"); !errors.Is(err, ErrPathNotFound) {
		t.Errorf("old path should be gone, got err=%v", err)
	}
	got, err := m.File("rn", "", "templates/c.yaml")
	if err != nil {
		t.Fatalf("new path missing: %v", err)
	}
	if decodeB64(t, got.ContentB64) != "A\n" {
		t.Errorf("content mismatch: %q", decodeB64(t, got.ContentB64))
	}
}

func TestCommitAutoMergeAcrossFiles(t *testing.T) {
	m := NewManager(t.TempDir())
	parent := seedBareWithFile(t, m, "am", "a.txt", "A\n")
	pushBareCommit(t, m, "am", map[string]string{"server.txt": "S\n"}, "server change")

	res, err := m.Commit("am", makeCommitOpts(parent, []Change{
		{Op: OpPut, Path: "client1.txt", Content: []byte("C1\n")},
		{Op: OpPut, Path: "client2.txt", Content: []byte("C2\n")},
	}, "client adds two files"))
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if res.MergedWith == "" {
		t.Error("expected auto-merge")
	}
	// All files present after merge.
	for _, p := range []string{"a.txt", "server.txt", "client1.txt", "client2.txt"} {
		if _, err := m.File("am", "", p); err != nil {
			t.Errorf("%s missing: %v", p, err)
		}
	}
}

func TestCommitConflictAbortsAll(t *testing.T) {
	m := NewManager(t.TempDir())
	parent := seedBareWithFile(t, m, "cf", "a.txt", "base\n")
	pushBareCommit(t, m, "cf", map[string]string{"a.txt": "server-edit\n"}, "server edit a")

	_, err := m.Commit("cf", makeCommitOpts(parent, []Change{
		{Op: OpPut, Path: "a.txt", Content: []byte("client-edit\n")},
		{Op: OpPut, Path: "b.txt", Content: []byte("client new\n")},
	}, "batch"))
	if err == nil {
		t.Fatal("expected conflict")
	}
	var ce *CommitConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("want CommitConflictError, got %T: %v", err, err)
	}
	if len(ce.Conflicts) != 1 || ce.Conflicts[0].Path != "a.txt" {
		t.Errorf("expected one conflict on a.txt, got %+v", ce.Conflicts)
	}
	c := ce.Conflicts[0]
	if c.BaseB64 == "" || c.TheirsB64 == "" || c.YoursB64 == "" {
		t.Errorf("expected full blob triple, got %+v", c)
	}
	// Transactional: b.txt must NOT exist after the failed commit.
	if _, err := m.File("cf", "", "b.txt"); !errors.Is(err, ErrPathNotFound) {
		t.Errorf("b.txt should not have been created on conflict; err=%v", err)
	}
}

func TestCommitStaleParentEchoesAllChanges(t *testing.T) {
	m := NewManager(t.TempDir())
	seedBareWithFile(t, m, "sp", "a.txt", "v1\n")

	// Plant an orphan commit (same trick as TestWriteFileStaleParent).
	otherSrc := seedSourceRepo(t, t.TempDir()+"/other")
	otherBare := NewManager(t.TempDir())
	if err := otherBare.CloneBare("other", otherSrc); err != nil {
		t.Fatalf("clone: %v", err)
	}
	orphan, _ := otherBare.Head("other")
	out, err := exec.Command("git", "-C", m.RepoPath("sp"),
		"fetch", otherBare.RepoPath("other"), orphan.Version).CombinedOutput()
	if err != nil {
		t.Fatalf("fetch: %s: %v", out, err)
	}

	_, err = m.Commit("sp", makeCommitOpts(orphan.Version, []Change{
		{Op: OpPut, Path: "x.txt", Content: []byte("new\n")},
		{Op: OpDelete, Path: "gone.txt"},
	}, "stale"))
	if err == nil {
		t.Fatal("expected conflict")
	}
	var ce *CommitConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("want CommitConflictError, got %T: %v", err, err)
	}
	if len(ce.Conflicts) != 2 {
		t.Errorf("expected 2 echoed conflicts, got %d", len(ce.Conflicts))
	}
	for _, c := range ce.Conflicts {
		if c.BaseB64 != "" || c.TheirsB64 != "" {
			t.Errorf("stale parent must not include base/theirs; got %+v", c)
		}
	}
}

func TestCommitBadOp(t *testing.T) {
	m := NewManager(t.TempDir())
	parent := seedBareWithFile(t, m, "bop", "a.txt", "A\n")
	_, err := m.Commit("bop", makeCommitOpts(parent, []Change{
		{Op: "nuke", Path: "a.txt"},
	}, "x"))
	if err == nil || !strings.Contains(err.Error(), "invalid op") {
		t.Errorf("want invalid-op error, got %v", err)
	}
}

func TestCommitEmptyChanges(t *testing.T) {
	m := NewManager(t.TempDir())
	parent := seedBareWithFile(t, m, "e", "a.txt", "A\n")
	_, err := m.Commit("e", makeCommitOpts(parent, nil, "empty"))
	if err == nil || !strings.Contains(err.Error(), "changes is required") {
		t.Errorf("want empty-changes error, got %v", err)
	}
}

func TestCommitBadParent(t *testing.T) {
	m := NewManager(t.TempDir())
	seedBareWithFile(t, m, "bp", "a.txt", "A\n")
	_, err := m.Commit("bp", makeCommitOpts(
		"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		[]Change{{Op: OpPut, Path: "a.txt", Content: []byte("x")}},
		"m"))
	if err != ErrVersionNotFound {
		t.Errorf("want ErrVersionNotFound, got %v", err)
	}
}

func TestCommitEmptyRepo(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.InitBare("fresh"); err != nil {
		t.Fatalf("init: %v", err)
	}
	_, err := m.Commit("fresh", makeCommitOpts("HEAD",
		[]Change{{Op: OpPut, Path: "a.txt", Content: []byte("x")}}, "m"))
	if err != ErrRepoEmpty {
		t.Errorf("want ErrRepoEmpty, got %v", err)
	}
}

func TestCommitInvalidPath(t *testing.T) {
	m := NewManager(t.TempDir())
	parent := seedBareWithFile(t, m, "ip", "a.txt", "A\n")
	_, err := m.Commit("ip", makeCommitOpts(parent,
		[]Change{{Op: OpPut, Path: ".git/config", Content: []byte("x")}}, "evil"))
	if !errors.Is(err, ErrInvalidPath) {
		t.Errorf("want ErrInvalidPath, got %v", err)
	}
}

func TestCommitSubscriptionTrailer(t *testing.T) {
	m := NewManager(t.TempDir())
	parent := seedBareWithFile(t, m, "tr", "a.txt", "A\n")
	opts := makeCommitOpts(parent,
		[]Change{{Op: OpPut, Path: "a.txt", Content: []byte("B\n")}}, "edit")
	opts.SubscriptionUsername = "alice"
	res, err := m.Commit("tr", opts)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	out, err := exec.Command("git", "-C", m.RepoPath("tr"),
		"log", "-1", "--format=%B", res.Version).Output()
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if !strings.Contains(string(out), "Subscription-Username: alice") {
		t.Errorf("trailer missing; got:\n%s", out)
	}
	_ = b64 // silence if unused in some subtests
}

func TestCommitMergeAuthorIsScaffolder(t *testing.T) {
	m := NewManager(t.TempDir())
	parent := seedBareWithFile(t, m, "ma", "a.txt", "A\n")
	pushBareCommit(t, m, "ma", map[string]string{"server.txt": "S\n"}, "server")

	opts := makeCommitOpts(parent,
		[]Change{{Op: OpPut, Path: "client.txt", Content: []byte("C\n")}}, "m")
	opts.SubscriptionUsername = "alice"
	res, err := m.Commit("ma", opts)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if res.MergedWith == "" {
		t.Fatal("expected auto-merge")
	}
	out, err := exec.Command("git", "-C", m.RepoPath("ma"),
		"log", "-1", "--format=%an|%ae|%cn|%ce", res.Version).Output()
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	fields := strings.Split(strings.TrimSpace(string(out)), "|")
	if fields[0] != testCommitter || fields[1] != testCommEmail {
		t.Errorf("merge author should be scaffolder, got %s <%s>", fields[0], fields[1])
	}
	if fields[2] != testCommitter || fields[3] != testCommEmail {
		t.Errorf("merge committer should be scaffolder, got %s <%s>", fields[2], fields[3])
	}
}
