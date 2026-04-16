package git

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// seedSourceRepo creates a non-bare git repo at dir with one committed file.
// Returns the repo path, suitable as a sourceURL for CloneBare.
func seedSourceRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := exec.Command("git", "init", dir).Run(); err != nil {
		t.Fatalf("git init source: %v", err)
	}
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	cmds := [][]string{
		{"-C", dir, "config", "user.email", "test@example.com"},
		{"-C", dir, "config", "user.name", "Test"},
		{"-C", dir, "add", "README.md"},
		{"-C", dir, "commit", "-m", "initial"},
	}
	for _, args := range cmds {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	return dir
}

func TestNewManager(t *testing.T) {
	m := NewManager("/tmp/repos")
	if m.repoRoot != "/tmp/repos" {
		t.Errorf("expected repoRoot /tmp/repos, got %s", m.repoRoot)
	}
}

func TestRepoPath(t *testing.T) {
	m := NewManager("/data/repos")
	got := m.RepoPath("myproject")
	expected := filepath.Join("/data/repos", "myproject.git")
	if got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
}

func TestInitBare(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	if err := m.InitBare("testrepo"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	repoDir := filepath.Join(dir, "testrepo.git")
	info, err := os.Stat(repoDir)
	if err != nil {
		t.Fatalf("repo dir should exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("repo path should be a directory")
	}
}

func TestInitBareDuplicate(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	if err := m.InitBare("dup"); err != nil {
		t.Fatalf("first init should succeed: %v", err)
	}

	err := m.InitBare("dup")
	if err == nil {
		t.Fatal("expected error for duplicate repo init")
	}
}

func TestListEmpty(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	repos, err := m.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 0 {
		t.Errorf("expected 0 repos, got %d", len(repos))
	}
}

func TestListNonExistentRoot(t *testing.T) {
	m := NewManager("/nonexistent/path")

	repos, err := m.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repos != nil {
		t.Errorf("expected nil for nonexistent root, got %v", repos)
	}
}

func TestListWithRepos(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	m.InitBare("alpha")
	m.InitBare("beta")

	// Create a non-.git dir that should be ignored.
	os.MkdirAll(filepath.Join(dir, "notarepo"), 0755)

	repos, err := m.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(repos))
	}

	names := map[string]bool{}
	for _, r := range repos {
		names[r] = true
	}
	if !names["alpha"] || !names["beta"] {
		t.Errorf("expected alpha and beta, got %v", repos)
	}
}

func TestDelete(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	m.InitBare("deleteme")
	if !m.Exists("deleteme") {
		t.Fatal("repo should exist before delete")
	}

	if err := m.Delete("deleteme"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m.Exists("deleteme") {
		t.Error("repo should not exist after delete")
	}
}

func TestDeleteNonExistent(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	err := m.Delete("ghost")
	if err == nil {
		t.Fatal("expected error for deleting nonexistent repo")
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	if m.Exists("nope") {
		t.Error("should not exist before init")
	}

	m.InitBare("yep")
	if !m.Exists("yep") {
		t.Error("should exist after init")
	}
}

func TestCloneBare(t *testing.T) {
	source := seedSourceRepo(t, filepath.Join(t.TempDir(), "source"))
	dest := t.TempDir()
	m := NewManager(dest)

	if err := m.CloneBare("mirror", source); err != nil {
		t.Fatalf("CloneBare: %v", err)
	}
	if !m.Exists("mirror") {
		t.Fatal("repo should exist after clone")
	}

	out, err := exec.Command("git", "-C", m.RepoPath("mirror"), "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("cloned repo should have HEAD: %v", err)
	}
	if strings.TrimSpace(string(out)) == "" {
		t.Fatal("cloned repo HEAD should be non-empty")
	}
}

func TestCloneBareEmptyURL(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.CloneBare("x", ""); err == nil {
		t.Fatal("expected error for empty source URL")
	}
}

func TestCloneBareDuplicate(t *testing.T) {
	source := seedSourceRepo(t, filepath.Join(t.TempDir(), "source"))
	m := NewManager(t.TempDir())

	if err := m.CloneBare("dup", source); err != nil {
		t.Fatalf("first clone should succeed: %v", err)
	}
	if err := m.CloneBare("dup", source); err == nil {
		t.Fatal("expected error for duplicate clone")
	}
}

func TestCloneBareInvalidSource(t *testing.T) {
	m := NewManager(t.TempDir())
	err := m.CloneBare("x", filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected error cloning from nonexistent source")
	}
	if m.Exists("x") {
		t.Error("repo should not exist after failed clone")
	}
}

func TestHeadEmptyRepo(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.InitBare("fresh"); err != nil {
		t.Fatalf("init: %v", err)
	}
	_, err := m.Head("fresh")
	if err == nil {
		t.Fatal("expected ErrRepoEmpty on empty repo")
	}
	if err != ErrRepoEmpty {
		t.Fatalf("want ErrRepoEmpty, got %v", err)
	}
}

func TestHeadPopulatedRepo(t *testing.T) {
	source := seedSourceRepo(t, filepath.Join(t.TempDir(), "source"))
	m := NewManager(t.TempDir())
	if err := m.CloneBare("cloned", source); err != nil {
		t.Fatalf("clone: %v", err)
	}

	info, err := m.Head("cloned")
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if len(info.Version) != 40 {
		t.Errorf("version should be a 40-char SHA, got %q", info.Version)
	}
	if info.DefaultBranch == "" {
		t.Error("default_branch should not be empty")
	}
}

func TestHeadMissingRepo(t *testing.T) {
	m := NewManager(t.TempDir())
	_, err := m.Head("nope")
	if err == nil {
		t.Fatal("expected error for missing repo")
	}
	if err == ErrRepoEmpty {
		t.Fatal("missing-repo error must not be ErrRepoEmpty")
	}
}

func TestTreeEmptyRepo(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.InitBare("fresh"); err != nil {
		t.Fatalf("init: %v", err)
	}
	_, err := m.Tree("fresh", "")
	if err != ErrRepoEmpty {
		t.Fatalf("want ErrRepoEmpty, got %v", err)
	}
}

func TestTreePopulated(t *testing.T) {
	source := seedSourceRepo(t, filepath.Join(t.TempDir(), "source"))
	m := NewManager(t.TempDir())
	if err := m.CloneBare("cloned", source); err != nil {
		t.Fatalf("clone: %v", err)
	}

	info, err := m.Tree("cloned", "")
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	if len(info.Version) != 40 {
		t.Errorf("version should be a 40-char SHA, got %q", info.Version)
	}
	if len(info.Files) != 1 {
		t.Fatalf("want 1 file, got %d: %+v", len(info.Files), info.Files)
	}
	e := info.Files[0]
	if e.Path != "README.md" {
		t.Errorf("path: want README.md, got %q", e.Path)
	}
	if e.Size != int64(len("hello\n")) {
		t.Errorf("size: want %d, got %d", len("hello\n"), e.Size)
	}
	if len(e.Blob) != 40 {
		t.Errorf("blob should be a 40-char SHA, got %q", e.Blob)
	}
}

func TestTreeBadVersion(t *testing.T) {
	source := seedSourceRepo(t, filepath.Join(t.TempDir(), "source"))
	m := NewManager(t.TempDir())
	if err := m.CloneBare("cloned", source); err != nil {
		t.Fatalf("clone: %v", err)
	}
	_, err := m.Tree("cloned", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err != ErrVersionNotFound {
		t.Fatalf("want ErrVersionNotFound, got %v", err)
	}
}

func TestTreeMissingRepo(t *testing.T) {
	m := NewManager(t.TempDir())
	_, err := m.Tree("nope", "")
	if err == nil {
		t.Fatal("expected error for missing repo")
	}
	if err == ErrRepoEmpty || err == ErrVersionNotFound {
		t.Fatalf("missing-repo must not be one of the sentinel errors; got %v", err)
	}
}

func TestSnapshotEmptyRepo(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.InitBare("fresh"); err != nil {
		t.Fatalf("init: %v", err)
	}
	_, err := m.Snapshot("fresh", "")
	if err != ErrRepoEmpty {
		t.Fatalf("want ErrRepoEmpty, got %v", err)
	}
}

func TestSnapshotPopulated(t *testing.T) {
	source := seedSourceRepo(t, filepath.Join(t.TempDir(), "source"))
	m := NewManager(t.TempDir())
	if err := m.CloneBare("cloned", source); err != nil {
		t.Fatalf("clone: %v", err)
	}

	info, err := m.Snapshot("cloned", "")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(info.Files) != 1 {
		t.Fatalf("want 1 file, got %d", len(info.Files))
	}
	got, err := base64.StdEncoding.DecodeString(info.Files[0].ContentB64)
	if err != nil {
		t.Fatalf("decode content: %v", err)
	}
	if string(got) != "hello\n" {
		t.Errorf("content: want %q, got %q", "hello\n", got)
	}
}

func TestSnapshotBadVersion(t *testing.T) {
	source := seedSourceRepo(t, filepath.Join(t.TempDir(), "source"))
	m := NewManager(t.TempDir())
	if err := m.CloneBare("cloned", source); err != nil {
		t.Fatalf("clone: %v", err)
	}
	_, err := m.Snapshot("cloned", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err != ErrVersionNotFound {
		t.Fatalf("want ErrVersionNotFound, got %v", err)
	}
}

func TestFileEmptyRepo(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.InitBare("fresh"); err != nil {
		t.Fatalf("init: %v", err)
	}
	_, err := m.File("fresh", "", "README.md")
	if err != ErrRepoEmpty {
		t.Fatalf("want ErrRepoEmpty, got %v", err)
	}
}

func TestFilePopulated(t *testing.T) {
	source := seedSourceRepo(t, filepath.Join(t.TempDir(), "source"))
	m := NewManager(t.TempDir())
	if err := m.CloneBare("cloned", source); err != nil {
		t.Fatalf("clone: %v", err)
	}

	info, err := m.File("cloned", "", "README.md")
	if err != nil {
		t.Fatalf("file: %v", err)
	}
	if info.Path != "README.md" {
		t.Errorf("path: want README.md, got %q", info.Path)
	}
	got, err := base64.StdEncoding.DecodeString(info.ContentB64)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(got) != "hello\n" {
		t.Errorf("content: want %q, got %q", "hello\n", got)
	}
}

func TestFileBadVersion(t *testing.T) {
	source := seedSourceRepo(t, filepath.Join(t.TempDir(), "source"))
	m := NewManager(t.TempDir())
	if err := m.CloneBare("cloned", source); err != nil {
		t.Fatalf("clone: %v", err)
	}
	_, err := m.File("cloned", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "README.md")
	if err != ErrVersionNotFound {
		t.Fatalf("want ErrVersionNotFound, got %v", err)
	}
}

func TestFilePathNotFound(t *testing.T) {
	source := seedSourceRepo(t, filepath.Join(t.TempDir(), "source"))
	m := NewManager(t.TempDir())
	if err := m.CloneBare("cloned", source); err != nil {
		t.Fatalf("clone: %v", err)
	}
	_, err := m.File("cloned", "", "does/not/exist.txt")
	if err != ErrPathNotFound {
		t.Fatalf("want ErrPathNotFound, got %v", err)
	}
}

func TestFileMissingRepo(t *testing.T) {
	m := NewManager(t.TempDir())
	_, err := m.File("nope", "", "README.md")
	if err == nil {
		t.Fatal("expected error for missing repo")
	}
	if err == ErrRepoEmpty || err == ErrVersionNotFound || err == ErrPathNotFound {
		t.Fatalf("missing-repo must not be a sentinel error; got %v", err)
	}
}
