package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ScaffoldFile is one file the scaffold should write into the initial commit.
type ScaffoldFile struct {
	Path    string // relative to the repo root (e.g. "templates/basic.yaml")
	Content []byte
}

// ScaffoldOptions controls a one-shot initial commit seeded into an otherwise
// empty bare repo. The scaffold clones the bare to a temp directory, writes
// the files, commits, pushes, then cleans up.
type ScaffoldOptions struct {
	CommitterName  string
	CommitterEmail string
	Message        string
	Files          []ScaffoldFile
	Branch         string // defaults to "master"
}

// Scaffold seeds the named bare repository with an initial commit. The repo
// must already exist (created via Manager.InitBare). Returns an error if the
// repo is non-empty — scaffolding is only valid on fresh repos.
func (m *Manager) Scaffold(name string, opts ScaffoldOptions) error {
	if len(opts.Files) == 0 {
		return fmt.Errorf("scaffold: no files to commit")
	}
	if opts.CommitterName == "" || opts.CommitterEmail == "" {
		return fmt.Errorf("scaffold: committer name and email required")
	}
	if opts.Message == "" {
		opts.Message = "Initial commit"
	}
	if opts.Branch == "" {
		opts.Branch = "master"
	}

	repoPath := m.RepoPath(name)
	if !m.Exists(name) {
		return fmt.Errorf("scaffold: repo %q does not exist", name)
	}

	// Refuse to scaffold a repo that already has commits. Mixing a scaffold
	// with existing history would rewrite it; much safer to require empty.
	if hasCommits, err := bareHasCommits(repoPath); err != nil {
		return fmt.Errorf("scaffold: inspect repo: %w", err)
	} else if hasCommits {
		return fmt.Errorf("scaffold: repo %q is not empty", name)
	}

	work, err := os.MkdirTemp("", "gigot-scaffold-*")
	if err != nil {
		return fmt.Errorf("scaffold: temp dir: %w", err)
	}
	defer os.RemoveAll(work)

	if out, err := exec.Command("git", "clone", repoPath, work).CombinedOutput(); err != nil {
		return fmt.Errorf("scaffold: clone: %s: %w", string(out), err)
	}

	for _, f := range opts.Files {
		p := filepath.Join(work, f.Path)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			return fmt.Errorf("scaffold: mkdir for %s: %w", f.Path, err)
		}
		if err := os.WriteFile(p, f.Content, 0644); err != nil {
			return fmt.Errorf("scaffold: write %s: %w", f.Path, err)
		}
	}

	gitIn := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME="+opts.CommitterName,
			"GIT_AUTHOR_EMAIL="+opts.CommitterEmail,
			"GIT_COMMITTER_NAME="+opts.CommitterName,
			"GIT_COMMITTER_EMAIL="+opts.CommitterEmail,
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %s: %w", args, string(out), err)
		}
		return nil
	}

	if err := gitIn("checkout", "-B", opts.Branch); err != nil {
		return fmt.Errorf("scaffold: checkout: %w", err)
	}
	if err := gitIn("add", "."); err != nil {
		return fmt.Errorf("scaffold: add: %w", err)
	}
	if err := gitIn("commit", "-m", opts.Message); err != nil {
		return fmt.Errorf("scaffold: commit: %w", err)
	}
	if err := gitIn("push", "origin", opts.Branch); err != nil {
		return fmt.Errorf("scaffold: push: %w", err)
	}
	return nil
}

func bareHasCommits(repoPath string) (bool, error) {
	cmd := exec.Command("git", "-C", repoPath, "rev-list", "--all", "--max-count=1")
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return len(out) > 0, nil
}
