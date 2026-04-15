package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func scaffoldTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	return NewManager(dir), dir
}

func newSeededRepo(t *testing.T, m *Manager, name string) {
	t.Helper()
	if err := m.InitBare(name); err != nil {
		t.Fatalf("init bare: %v", err)
	}
}

func TestScaffold_CommitAppearsInBareRepo(t *testing.T) {
	m, _ := scaffoldTestManager(t)
	newSeededRepo(t, m, "seedling")

	files := []ScaffoldFile{
		{Path: "README.md", Content: []byte("hello\n")},
		{Path: "templates/basic.yaml", Content: []byte("name: basic\n")},
		{Path: "storage/.gitkeep", Content: []byte{}},
	}
	err := m.Scaffold("seedling", ScaffoldOptions{
		CommitterName:  "GiGot Scaffolder",
		CommitterEmail: "scaffold@gigot.local",
		Message:        "seed",
		Files:          files,
	})
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	repoPath := m.RepoPath("seedling")
	out, err := exec.Command("git", "-C", repoPath, "log", "--pretty=format:%an|%ae|%s").Output()
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if want := "GiGot Scaffolder|scaffold@gigot.local|seed"; string(out) != want {
		t.Fatalf("log = %q, want %q", string(out), want)
	}

	ls, _ := exec.Command("git", "-C", repoPath, "ls-tree", "-r", "HEAD", "--name-only").Output()
	lsStr := string(ls)
	for _, expect := range []string{"README.md", "templates/basic.yaml", "storage/.gitkeep"} {
		if !strings.Contains(lsStr, expect) {
			t.Fatalf("ls-tree missing %q; got:\n%s", expect, lsStr)
		}
	}
}

func TestScaffold_RefusesWhenRepoHasCommits(t *testing.T) {
	m, _ := scaffoldTestManager(t)
	newSeededRepo(t, m, "already-has-stuff")

	first := []ScaffoldFile{{Path: "one.txt", Content: []byte("x")}}
	if err := m.Scaffold("already-has-stuff", ScaffoldOptions{
		CommitterName: "a", CommitterEmail: "a@a", Message: "first", Files: first,
	}); err != nil {
		t.Fatal(err)
	}

	err := m.Scaffold("already-has-stuff", ScaffoldOptions{
		CommitterName: "b", CommitterEmail: "b@b", Message: "second", Files: first,
	})
	if err == nil {
		t.Fatal("second scaffold should be refused on a non-empty repo")
	}
}

func TestScaffold_RefusesWhenRepoMissing(t *testing.T) {
	m, _ := scaffoldTestManager(t)
	err := m.Scaffold("nope", ScaffoldOptions{
		CommitterName: "a", CommitterEmail: "a@a", Message: "m",
		Files: []ScaffoldFile{{Path: "x", Content: []byte("x")}},
	})
	if err == nil {
		t.Fatal("scaffold should refuse when repo does not exist")
	}
}

func TestScaffold_RequiresFiles(t *testing.T) {
	m, _ := scaffoldTestManager(t)
	newSeededRepo(t, m, "empty-seed")
	err := m.Scaffold("empty-seed", ScaffoldOptions{
		CommitterName: "a", CommitterEmail: "a@a",
	})
	if err == nil {
		t.Fatal("scaffold should refuse with no files")
	}
}

func TestScaffold_RequiresCommitterIdentity(t *testing.T) {
	m, _ := scaffoldTestManager(t)
	newSeededRepo(t, m, "needs-id")
	err := m.Scaffold("needs-id", ScaffoldOptions{
		Files: []ScaffoldFile{{Path: "x", Content: []byte("x")}},
	})
	if err == nil {
		t.Fatal("scaffold should refuse without committer identity")
	}
}

func TestScaffold_CreatesNestedDirectories(t *testing.T) {
	m, _ := scaffoldTestManager(t)
	newSeededRepo(t, m, "nested")
	err := m.Scaffold("nested", ScaffoldOptions{
		CommitterName:  "a",
		CommitterEmail: "a@a",
		Message:        "deep",
		Files: []ScaffoldFile{
			{Path: "a/b/c/deep.txt", Content: []byte("x")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Clone the bare back out to confirm the nested file is reachable.
	clone := filepath.Join(t.TempDir(), "clone")
	if err := exec.Command("git", "clone", m.RepoPath("nested"), clone).Run(); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(clone, "a", "b", "c", "deep.txt")); err != nil {
		t.Fatalf("nested file missing after clone: %v", err)
	}
}
