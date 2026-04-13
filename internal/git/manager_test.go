package git

import (
	"os"
	"path/filepath"
	"testing"
)

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
