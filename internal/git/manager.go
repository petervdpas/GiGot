package git

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrVersionNotFound is returned when a caller-supplied version (commit SHA,
// ref name, etc.) does not resolve in the target repo.
var ErrVersionNotFound = errors.New("version not found")

// ErrPathNotFound is returned when a path does not exist at the requested
// version (the version itself resolves fine).
var ErrPathNotFound = errors.New("path not found at this version")

// ErrRepoEmpty is returned when an operation needs HEAD but the repo has no
// commits yet (freshly initialised, not yet scaffolded or pushed to).
var ErrRepoEmpty = errors.New("repository has no commits")

// HeadInfo describes the current HEAD of a repository: the commit SHA the
// branch points at, plus the branch name itself.
type HeadInfo struct {
	Version       string `json:"version"`
	DefaultBranch string `json:"default_branch"`
}

// TreeEntry describes one blob at a given version.
type TreeEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Blob string `json:"blob"`
}

// TreeInfo is the full recursive listing of a tree at a given commit.
type TreeInfo struct {
	Version string      `json:"version"`
	Files   []TreeEntry `json:"files"`
}

// Tree returns the recursive blob listing of the given version. An empty
// version defaults to HEAD — in which case an empty repo surfaces as
// ErrRepoEmpty. An unresolvable version returns ErrVersionNotFound.
func (m *Manager) Tree(name, version string) (TreeInfo, error) {
	if !m.Exists(name) {
		return TreeInfo{}, fmt.Errorf("repository %q does not exist", name)
	}
	path := m.RepoPath(name)

	resolved := version
	if resolved == "" {
		head, err := m.Head(name)
		if err != nil {
			return TreeInfo{}, err
		}
		resolved = head.Version
	}

	out, err := exec.Command("git", "-C", path, "ls-tree", "-r", "-l", resolved).Output()
	if err != nil {
		return TreeInfo{}, ErrVersionNotFound
	}

	var files []TreeEntry
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		// Line format: "<mode> <type> <sha> <size>\t<path>". Path may contain
		// spaces; everything before the tab is fixed-width whitespace-split.
		tab := strings.SplitN(line, "\t", 2)
		if len(tab) != 2 {
			continue
		}
		header, p := tab[0], tab[1]
		parts := strings.Fields(header)
		if len(parts) < 4 {
			continue
		}
		size, _ := strconv.ParseInt(parts[3], 10, 64)
		files = append(files, TreeEntry{
			Path: p,
			Size: size,
			Blob: parts[2],
		})
	}
	return TreeInfo{Version: resolved, Files: files}, nil
}

// SnapshotFile is one blob's content at a version, base64-encoded for JSON
// transport.
type SnapshotFile struct {
	Path       string `json:"path"`
	ContentB64 string `json:"content_b64"`
}

// SnapshotInfo is the full content dump of a tree at a version, intended for
// initial client populate and disaster recovery (see
// docs/design/structured-sync-api.md §3.3).
type SnapshotInfo struct {
	Version string         `json:"version"`
	Files   []SnapshotFile `json:"files"`
}

// Snapshot returns every blob at the given version with its content
// base64-encoded. Delegates tree resolution to Tree, so empty repos surface
// as ErrRepoEmpty and unresolvable versions as ErrVersionNotFound.
func (m *Manager) Snapshot(name, version string) (SnapshotInfo, error) {
	tree, err := m.Tree(name, version)
	if err != nil {
		return SnapshotInfo{}, err
	}
	path := m.RepoPath(name)
	files := make([]SnapshotFile, 0, len(tree.Files))
	for _, entry := range tree.Files {
		blob, err := exec.Command("git", "-C", path, "cat-file", "blob", entry.Blob).Output()
		if err != nil {
			return SnapshotInfo{}, fmt.Errorf("cat-file %s: %w", entry.Blob, err)
		}
		files = append(files, SnapshotFile{
			Path:       entry.Path,
			ContentB64: base64.StdEncoding.EncodeToString(blob),
		})
	}
	return SnapshotInfo{Version: tree.Version, Files: files}, nil
}

// FileInfo is a single blob at a version, base64-encoded for JSON transport.
type FileInfo struct {
	Version    string `json:"version"`
	Path       string `json:"path"`
	ContentB64 string `json:"content_b64"`
}

// File returns one blob's content at the given version. An empty version
// defaults to HEAD (bubbling ErrRepoEmpty on an empty repo). An unresolvable
// version returns ErrVersionNotFound; a version that resolves but lacks the
// path returns ErrPathNotFound.
func (m *Manager) File(name, version, path string) (FileInfo, error) {
	if !m.Exists(name) {
		return FileInfo{}, fmt.Errorf("repository %q does not exist", name)
	}
	repoPath := m.RepoPath(name)

	resolved := version
	if resolved == "" {
		head, err := m.Head(name)
		if err != nil {
			return FileInfo{}, err
		}
		resolved = head.Version
	}

	// Verify the version resolves separately so we can tell a bad version
	// apart from a missing path in the error path below.
	if err := exec.Command("git", "-C", repoPath, "rev-parse", "--verify", resolved+"^{commit}").Run(); err != nil {
		return FileInfo{}, ErrVersionNotFound
	}

	blob, err := exec.Command("git", "-C", repoPath, "cat-file", "blob", resolved+":"+path).Output()
	if err != nil {
		return FileInfo{}, ErrPathNotFound
	}
	return FileInfo{
		Version:    resolved,
		Path:       path,
		ContentB64: base64.StdEncoding.EncodeToString(blob),
	}, nil
}

// Head returns the current HEAD commit SHA and the branch name HEAD points
// at. Returns ErrRepoEmpty if the repo has no commits yet.
func (m *Manager) Head(name string) (HeadInfo, error) {
	path := m.RepoPath(name)
	if !m.Exists(name) {
		return HeadInfo{}, fmt.Errorf("repository %q does not exist", name)
	}

	branchOut, err := exec.Command("git", "-C", path, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil {
		return HeadInfo{}, fmt.Errorf("symbolic-ref HEAD: %w", err)
	}
	branch := strings.TrimSpace(string(branchOut))

	// --verify fails cleanly when HEAD points at a branch with no commits;
	// without --verify, git rev-parse just echoes back the literal "HEAD".
	shaOut, err := exec.Command("git", "-C", path, "rev-parse", "--verify", "HEAD").Output()
	if err != nil {
		return HeadInfo{}, ErrRepoEmpty
	}
	return HeadInfo{
		Version:       strings.TrimSpace(string(shaOut)),
		DefaultBranch: branch,
	}, nil
}

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

	m.enableReceivePack(path)
	return nil
}

// CloneBare clones an external git repository as a bare repo under the
// manager's root. sourceURL is passed to `git clone` as-is, so any form git
// accepts (http(s), git, ssh, local path) works; transport restrictions on
// the host git still apply (e.g. file:// is blocked by default on git ≥2.38).
func (m *Manager) CloneBare(name, sourceURL string) error {
	if sourceURL == "" {
		return fmt.Errorf("source URL is required")
	}
	path := m.RepoPath(name)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("repository %q already exists", name)
	}

	cmd := exec.Command("git", "clone", "--bare", sourceURL, path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone --bare: %s: %w", strings.TrimSpace(string(out)), err)
	}

	m.enableReceivePack(path)
	return nil
}

// enableReceivePack flips http.receivepack on so push over HTTP works. Best-
// effort — failure here is non-fatal for the caller.
func (m *Manager) enableReceivePack(path string) {
	exec.Command("git", "-C", path, "config", "http.receivepack", "true").Run()
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
