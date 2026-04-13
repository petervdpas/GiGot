package git

import (
	"fmt"
	"os"
	"path/filepath"
)

// Manager handles bare git repository operations on disk.
type Manager struct {
	repoRoot string
}

// NewManager creates a Manager rooted at the given directory.
func NewManager(repoRoot string) *Manager {
	return &Manager{repoRoot: repoRoot}
}

// RepoPath returns the absolute path for a named repo.
func (m *Manager) RepoPath(name string) string {
	return filepath.Join(m.repoRoot, name+".git")
}

// InitBare creates a new bare git repository.
func (m *Manager) InitBare(name string) error {
	path := m.RepoPath(name)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("repository %q already exists", name)
	}
	return os.MkdirAll(path, 0755)
	// TODO: initialize bare repo structure (HEAD, objects, refs)
}

// List returns the names of all repositories.
func (m *Manager) List() ([]string, error) {
	entries, err := os.ReadDir(m.repoRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var repos []string
	for _, e := range entries {
		if e.IsDir() && filepath.Ext(e.Name()) == ".git" {
			name := e.Name()[:len(e.Name())-4]
			repos = append(repos, name)
		}
	}
	return repos, nil
}

// Exists checks whether a named repository exists.
func (m *Manager) Exists(name string) bool {
	_, err := os.Stat(m.RepoPath(name))
	return err == nil
}

// Delete removes a repository from disk.
func (m *Manager) Delete(name string) error {
	path := m.RepoPath(name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("repository %q does not exist", name)
	}
	return os.RemoveAll(path)
}
