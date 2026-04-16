package git

import (
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
