package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Manager handles bare git repository operations on disk.
type Manager struct {
	repoRoot string
}

// NewManager creates a Manager rooted at the given directory.
func NewManager(repoRoot string) *Manager {
	return &Manager{repoRoot: repoRoot}
}

// RepoRoot returns the root directory for all repositories.
func (m *Manager) RepoRoot() string {
	return m.repoRoot
}

// RepoPath returns the absolute path for a named repo.
func (m *Manager) RepoPath(name string) string {
	return filepath.Join(m.repoRoot, name+".git")
}

// InitBare creates a new bare git repository using git init --bare.
func (m *Manager) InitBare(name string) error {
	path := m.RepoPath(name)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("repository %q already exists", name)
	}

	cmd := exec.Command("git", "init", "--bare", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git init --bare: %s: %w", string(out), err)
	}

	// Enable the http.receivepack config so push over HTTP works.
	cfg := exec.Command("git", "-C", path, "config", "http.receivepack", "true")
	cfg.Run()

	return nil
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

// BranchInfo describes a git branch.
type BranchInfo struct {
	Name   string `json:"name"`
	Head   string `json:"head"`
	Active bool   `json:"active"`
}

// Branches returns the list of branches in a repository.
func (m *Manager) Branches(name string) ([]BranchInfo, error) {
	path := m.RepoPath(name)
	if !m.Exists(name) {
		return nil, fmt.Errorf("repository %q does not exist", name)
	}

	cmd := exec.Command("git", "-C", path, "branch", "--format=%(refname:short) %(objectname:short)")
	out, err := cmd.Output()
	if err != nil {
		return nil, nil // empty repo, no branches yet
	}

	// Get HEAD ref.
	headCmd := exec.Command("git", "-C", path, "symbolic-ref", "--short", "HEAD")
	headOut, _ := headCmd.Output()
	headBranch := strings.TrimSpace(string(headOut))

	var branches []BranchInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		b := BranchInfo{Name: parts[0], Active: parts[0] == headBranch}
		if len(parts) == 2 {
			b.Head = parts[1]
		}
		branches = append(branches, b)
	}
	return branches, nil
}

// LogEntry describes a single commit.
type LogEntry struct {
	Hash    string `json:"hash"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	Message string `json:"message"`
}

// Log returns recent commits from a repository.
func (m *Manager) Log(name string, limit int) ([]LogEntry, error) {
	path := m.RepoPath(name)
	if !m.Exists(name) {
		return nil, fmt.Errorf("repository %q does not exist", name)
	}

	if limit <= 0 {
		limit = 20
	}

	cmd := exec.Command("git", "-C", path, "log",
		fmt.Sprintf("--max-count=%d", limit),
		"--format=%H|%an|%ai|%s",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, nil // empty repo, no commits
	}

	var entries []LogEntry
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 4 {
			continue
		}
		entries = append(entries, LogEntry{
			Hash:    parts[0],
			Author:  parts[1],
			Date:    parts[2],
			Message: parts[3],
		})
	}
	return entries, nil
}
