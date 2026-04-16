package git

import (
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const (
	testAuthor    = "Alice"
	testEmail     = "alice@example.com"
	testCommitter = "GiGot Scaffolder"
	testCommEmail = "scaffold@gigot.local"
)

// seedBareWithFile creates a bare repo and seeds one commit holding path=content.
// Returns the initial HEAD SHA.
func seedBareWithFile(t *testing.T, m *Manager, name, path, content string) string {
	t.Helper()
	if err := m.InitBare(name); err != nil {
		t.Fatalf("init: %v", err)
	}
	err := m.Scaffold(name, ScaffoldOptions{
		CommitterName:  testCommitter,
		CommitterEmail: testCommEmail,
		Message:        "seed",
		Files:          []ScaffoldFile{{Path: path, Content: []byte(content)}},
	})
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	head, err := m.Head(name)
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	return head.Version
}

// pushBareCommit clones the bare, writes files (overwriting/creating as
// needed), and pushes one commit. Returns the new HEAD SHA. Used to simulate
// a concurrent server-side commit without going through WriteFile (keeping
// tests of WriteFile independent of WriteFile itself as seed machinery).
func pushBareCommit(t *testing.T, m *Manager, name string, files map[string]string, msg string) string {
	t.Helper()
	repoPath := m.RepoPath(name)
	work := filepath.Join(t.TempDir(), "work")
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("clone", repoPath, work)
	run("-C", work, "config", "user.email", testCommEmail)
	run("-C", work, "config", "user.name", testCommitter)

	for p, c := range files {
		full := filepath.Join(work, p)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(c), 0644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
		run("-C", work, "add", p)
	}
	run("-C", work, "commit", "-m", msg)
	run("-C", work, "push", "origin", "master")

	head, err := m.Head(name)
	if err != nil {
		t.Fatalf("head after push: %v", err)
	}
	return head.Version
}

func makeWriteOpts(parent, path, content, message string) WriteOptions {
	return WriteOptions{
		ParentVersion:  parent,
		Path:           path,
		Content:        []byte(content),
		AuthorName:     testAuthor,
		AuthorEmail:    testEmail,
		CommitterName:  testCommitter,
		CommitterEmail: testCommEmail,
		Message:        message,
	}
}

func TestWriteFileFastForward(t *testing.T) {
	m := NewManager(t.TempDir())
	parent := seedBareWithFile(t, m, "ff", "a.txt", "one\n")

	res, err := m.WriteFile("ff", makeWriteOpts(parent, "a.txt", "two\n", "edit"))
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !res.FastForward {
		t.Error("expected FastForward=true")
	}
	if res.MergedFrom != "" || res.MergedWith != "" {
		t.Errorf("fast-forward should not populate merge fields: %+v", res)
	}
	head, _ := m.Head("ff")
	if head.Version != res.Version {
		t.Errorf("HEAD not advanced: head=%s result=%s", head.Version, res.Version)
	}

	got, err := m.File("ff", "", "a.txt")
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	decoded, _ := base64.StdEncoding.DecodeString(got.ContentB64)
	if string(decoded) != "two\n" {
		t.Errorf("content: want %q, got %q", "two\n", decoded)
	}
}

func TestWriteFileAutoMergeDifferentFiles(t *testing.T) {
	m := NewManager(t.TempDir())
	parent := seedBareWithFile(t, m, "m1", "a.txt", "A\n")
	pushBareCommit(t, m, "m1", map[string]string{"b.txt": "B\n"}, "add b")

	res, err := m.WriteFile("m1", makeWriteOpts(parent, "a.txt", "A edited\n", "edit a"))
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if res.FastForward {
		t.Error("should have merged, not fast-forwarded")
	}
	if res.MergedFrom != parent {
		t.Errorf("MergedFrom: want %s, got %s", parent, res.MergedFrom)
	}
	if res.MergedWith == "" {
		t.Error("MergedWith should be populated")
	}

	// After merge, both files exist.
	a, _ := m.File("m1", "", "a.txt")
	aDec, _ := base64.StdEncoding.DecodeString(a.ContentB64)
	if string(aDec) != "A edited\n" {
		t.Errorf("a.txt: want %q, got %q", "A edited\n", aDec)
	}
	b, _ := m.File("m1", "", "b.txt")
	bDec, _ := base64.StdEncoding.DecodeString(b.ContentB64)
	if string(bDec) != "B\n" {
		t.Errorf("b.txt: want %q, got %q", "B\n", bDec)
	}
}

func TestWriteFileAutoMergeNonOverlapping(t *testing.T) {
	m := NewManager(t.TempDir())
	base := "line1\nline2\nline3\nline4\nline5\n"
	parent := seedBareWithFile(t, m, "m2", "a.txt", base)
	// Server modifies the last line.
	pushBareCommit(t, m, "m2", map[string]string{
		"a.txt": "line1\nline2\nline3\nline4\nLINE5\n",
	}, "server edit tail")

	// Client (parent) modifies the first line — non-overlapping.
	clientContent := "LINE1\nline2\nline3\nline4\nline5\n"
	res, err := m.WriteFile("m2", makeWriteOpts(parent, "a.txt", clientContent, "edit head"))
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if res.FastForward {
		t.Error("should have merged, not fast-forwarded")
	}
	got, _ := m.File("m2", "", "a.txt")
	dec, _ := base64.StdEncoding.DecodeString(got.ContentB64)
	want := "LINE1\nline2\nline3\nline4\nLINE5\n"
	if string(dec) != want {
		t.Errorf("merged content: want %q, got %q", want, dec)
	}
}

func TestWriteFileConflict(t *testing.T) {
	m := NewManager(t.TempDir())
	parent := seedBareWithFile(t, m, "cf", "a.txt", "original\n")
	pushBareCommit(t, m, "cf", map[string]string{"a.txt": "server-change\n"}, "server edit")

	_, err := m.WriteFile("cf", makeWriteOpts(parent, "a.txt", "client-change\n", "client edit"))
	if err == nil {
		t.Fatal("expected conflict error")
	}
	var ce *WriteConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("want WriteConflictError, got %T: %v", err, err)
	}
	c := ce.Conflict
	if c.Path != "a.txt" {
		t.Errorf("Path: want a.txt, got %q", c.Path)
	}
	if c.BaseB64 == "" || c.TheirsB64 == "" || c.YoursB64 == "" {
		t.Errorf("expected all three blobs populated; got %+v", c)
	}
	decode := func(s string) string {
		b, _ := base64.StdEncoding.DecodeString(s)
		return string(b)
	}
	if decode(c.BaseB64) != "original\n" {
		t.Errorf("base: want %q, got %q", "original\n", decode(c.BaseB64))
	}
	if decode(c.TheirsB64) != "server-change\n" {
		t.Errorf("theirs: want %q, got %q", "server-change\n", decode(c.TheirsB64))
	}
	if decode(c.YoursB64) != "client-change\n" {
		t.Errorf("yours: want %q, got %q", "client-change\n", decode(c.YoursB64))
	}
}

func TestWriteFileStaleParent(t *testing.T) {
	m := NewManager(t.TempDir())
	seedBareWithFile(t, m, "sp", "a.txt", "v1\n")

	// Use a bogus SHA that still resolves (another commit from an unrelated
	// history). Cheapest way: point another bare repo at a different history
	// and use its HEAD.
	otherSrc := seedSourceRepo(t, filepath.Join(t.TempDir(), "other"))
	otherBare := NewManager(t.TempDir())
	if err := otherBare.CloneBare("other", otherSrc); err != nil {
		t.Fatalf("CloneBare other: %v", err)
	}
	otherHead, _ := otherBare.Head("other")

	// Plant that commit into our repo's object store without making it
	// reachable from HEAD. `git fetch` from a local bare works for this.
	repoPath := m.RepoPath("sp")
	out, err := exec.Command("git", "-C", repoPath, "fetch", otherBare.RepoPath("other"), otherHead.Version).CombinedOutput()
	if err != nil {
		t.Fatalf("fetch other: %s: %v", out, err)
	}

	_, err = m.WriteFile("sp", makeWriteOpts(otherHead.Version, "a.txt", "client\n", "edit"))
	if err == nil {
		t.Fatal("expected conflict error")
	}
	var ce *WriteConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("want WriteConflictError, got %T: %v", err, err)
	}
	if ce.Conflict.BaseB64 != "" || ce.Conflict.TheirsB64 != "" {
		t.Errorf("stale-parent 409 should not carry base/theirs; got %+v", ce.Conflict)
	}
	if ce.Conflict.YoursB64 == "" {
		t.Error("stale-parent 409 should echo yours back")
	}
}

func TestWriteFileBadParent(t *testing.T) {
	m := NewManager(t.TempDir())
	seedBareWithFile(t, m, "bp", "a.txt", "v1\n")

	_, err := m.WriteFile("bp", makeWriteOpts(
		"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "a.txt", "x\n", "m"))
	if err != ErrVersionNotFound {
		t.Fatalf("want ErrVersionNotFound, got %v", err)
	}
}

func TestWriteFileEmptyRepo(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.InitBare("fresh"); err != nil {
		t.Fatalf("init: %v", err)
	}
	_, err := m.WriteFile("fresh", makeWriteOpts("HEAD", "a.txt", "x\n", "m"))
	if err != ErrRepoEmpty {
		t.Fatalf("want ErrRepoEmpty, got %v", err)
	}
}

func TestWriteFileMissingRepo(t *testing.T) {
	m := NewManager(t.TempDir())
	_, err := m.WriteFile("ghost", makeWriteOpts("HEAD", "a.txt", "x\n", "m"))
	if err == nil {
		t.Fatal("expected missing-repo error")
	}
	if err == ErrRepoEmpty || err == ErrVersionNotFound {
		t.Fatalf("missing-repo must not be a sentinel; got %v", err)
	}
}

func TestWriteFileInvalidPath(t *testing.T) {
	m := NewManager(t.TempDir())
	parent := seedBareWithFile(t, m, "ip", "a.txt", "A\n")

	for _, p := range []string{"../escape.txt", "/abs.txt", "a/../../b.txt"} {
		_, err := m.WriteFile("ip", makeWriteOpts(parent, p, "x\n", "m"))
		if !errors.Is(err, ErrInvalidPath) {
			t.Errorf("path %q: want ErrInvalidPath, got %v", p, err)
		}
	}
}

func TestWriteFileSubscriptionTrailer(t *testing.T) {
	m := NewManager(t.TempDir())
	parent := seedBareWithFile(t, m, "tr", "a.txt", "A\n")

	opts := makeWriteOpts(parent, "a.txt", "B\n", "edit")
	opts.SubscriptionUsername = "alice"
	res, err := m.WriteFile("tr", opts)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	out, err := exec.Command("git", "-C", m.RepoPath("tr"),
		"log", "-1", "--format=%B", res.Version).Output()
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if !strings.Contains(string(out), "Subscription-Username: alice") {
		t.Errorf("commit message missing trailer; got:\n%s", out)
	}
}

func TestWriteFileMergeCommitAuthorIsScaffolder(t *testing.T) {
	m := NewManager(t.TempDir())
	parent := seedBareWithFile(t, m, "ma", "a.txt", "A\n")
	pushBareCommit(t, m, "ma", map[string]string{"b.txt": "B\n"}, "server")

	opts := makeWriteOpts(parent, "a.txt", "A edited\n", "edit")
	opts.SubscriptionUsername = "alice"
	res, err := m.WriteFile("ma", opts)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if res.MergedWith == "" {
		t.Fatal("expected a merge commit")
	}
	out, err := exec.Command("git", "-C", m.RepoPath("ma"),
		"log", "-1", "--format=%an|%ae|%cn|%ce", res.Version).Output()
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	fields := strings.Split(strings.TrimSpace(string(out)), "|")
	if len(fields) != 4 {
		t.Fatalf("unexpected format: %q", out)
	}
	an, ae, cn, ce := fields[0], fields[1], fields[2], fields[3]
	if an != testCommitter || ae != testCommEmail {
		t.Errorf("merge author should be scaffolder, got %s <%s>", an, ae)
	}
	if cn != testCommitter || ce != testCommEmail {
		t.Errorf("merge committer should be scaffolder, got %s <%s>", cn, ce)
	}
}

// TestWriteFileConcurrent races two writers against the same parent on
// different files. Exactly one fast-forwards; the other's CAS loses, so it
// must take the auto-merge path. The final HEAD holds both edits and is a
// merge commit with two parents — matching the Phase 2 acceptance criterion
// in docs/design/structured-sync-api.md §6.
func TestWriteFileConcurrent(t *testing.T) {
	m := NewManager(t.TempDir())
	parent := seedBareWithFile(t, m, "race", "seed.txt", "seed\n")

	var wg sync.WaitGroup
	errs := make([]error, 2)
	results := make([]WriteResult, 2)
	start := make(chan struct{})

	for i, path := range []string{"a.txt", "b.txt"} {
		i, path := i, path
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			opts := makeWriteOpts(parent, path, "content\n", "edit "+path)
			opts.SubscriptionUsername = "writer" + path
			results[i], errs[i] = m.WriteFile("race", opts)
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d failed: %v", i, err)
		}
	}
	// Exactly one fast-forward, exactly one merge.
	ffCount, mergeCount := 0, 0
	for _, r := range results {
		if r.FastForward {
			ffCount++
		}
		if r.MergedWith != "" {
			mergeCount++
		}
	}
	if ffCount != 1 || mergeCount != 1 {
		t.Fatalf("want 1 ff + 1 merge, got ff=%d merge=%d (%+v)", ffCount, mergeCount, results)
	}

	// Final HEAD should be the merge commit with two parents.
	head, _ := m.Head("race")
	parentsOut, err := exec.Command("git", "-C", m.RepoPath("race"),
		"rev-list", "--parents", "-n", "1", head.Version).Output()
	if err != nil {
		t.Fatalf("rev-list: %v", err)
	}
	parts := strings.Fields(strings.TrimSpace(string(parentsOut)))
	if len(parts) != 3 {
		t.Errorf("HEAD should have exactly 2 parents, got %d: %v", len(parts)-1, parts)
	}

	// Both files must exist, no data loss.
	for _, path := range []string{"a.txt", "b.txt", "seed.txt"} {
		if _, err := m.File("race", "", path); err != nil {
			t.Errorf("file %s missing at HEAD: %v", path, err)
		}
	}
}

func TestWriteFileCreatesNewPath(t *testing.T) {
	m := NewManager(t.TempDir())
	parent := seedBareWithFile(t, m, "np", "a.txt", "A\n")

	res, err := m.WriteFile("np", makeWriteOpts(parent, "b/c.txt", "new\n", "add b/c"))
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !res.FastForward {
		t.Error("adding a new file on top of HEAD should fast-forward")
	}
	got, err := m.File("np", "", "b/c.txt")
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	dec, _ := base64.StdEncoding.DecodeString(got.ContentB64)
	if string(dec) != "new\n" {
		t.Errorf("content: want %q, got %q", "new\n", dec)
	}
}
