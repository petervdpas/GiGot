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
	_ = m.installAuditGuard(path)
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
	_ = m.installAuditGuard(path)
	return nil
}

// enableReceivePack flips http.receivepack on so push over HTTP works. Best-
// effort — failure here is non-fatal for the caller.
func (m *Manager) enableReceivePack(path string) {
	exec.Command("git", "-C", path, "config", "http.receivepack", "true").Run()
}

// auditGuardHook is the POSIX-sh pre-receive hook that refuses any client
// push whose refname lives under refs/audit/*. Server-side writes via
// update-ref (AppendAudit) bypass hooks and are unaffected. Kept as a tiny
// inline script so the installation is a single file write with no
// dependencies on the host toolchain beyond /bin/sh.
const auditGuardHook = `#!/bin/sh
# GiGot: reject client pushes to refs/audit/* (server-owned).
while read -r _old _new ref; do
    case "$ref" in
        refs/audit/*)
            echo "GiGot: refs/audit/* is server-owned; rejecting $ref" >&2
            exit 1
            ;;
    esac
done
`

// installAuditGuard writes hooks/pre-receive into the named bare repo.
// Overwrites any prior content because the guard is load-bearing for the
// audit-trail tamper-proof property — a hand-edited hook is always wrong.
// Best-effort — failure here is non-fatal for the caller, but is surfaced
// as a returned error so callers who care (EnsureAuditGuards) can report
// partial failure.
func (m *Manager) installAuditGuard(repoPath string) error {
	hooksDir := filepath.Join(repoPath, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return fmt.Errorf("mkdir hooks: %w", err)
	}
	hookPath := filepath.Join(hooksDir, "pre-receive")
	if err := os.WriteFile(hookPath, []byte(auditGuardHook), 0o755); err != nil {
		return fmt.Errorf("write pre-receive: %w", err)
	}
	return nil
}

// EnsureAuditGuards re-installs the audit-guard hook on every repo under
// the manager's root. Intended to run once at server start so repos
// created before the guard existed are migrated without operator work.
// Repo-level failures are collected and returned joined; a successful
// return means every repo is guarded.
func (m *Manager) EnsureAuditGuards() error {
	names, err := m.List()
	if err != nil {
		return fmt.Errorf("list repos: %w", err)
	}
	var failures []string
	for _, name := range names {
		if err := m.installAuditGuard(m.RepoPath(name)); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", name, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("install audit guard failed on %d repo(s): %s",
			len(failures), strings.Join(failures, "; "))
	}
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

// CommitCount returns the number of commits reachable from HEAD.
// An empty repo reports 0 rather than an error, so callers rendering a
// "repo card" can display "0 commits" without special-casing.
func (m *Manager) CommitCount(name string) (int, error) {
	path := m.RepoPath(name)
	if !m.Exists(name) {
		return 0, fmt.Errorf("repository %q does not exist", name)
	}
	out, err := exec.Command("git", "-C", path, "rev-list", "--count", "HEAD").Output()
	if err != nil {
		// rev-list exits non-zero on repos with no commits — that's "0",
		// not an error condition from the caller's perspective.
		return 0, nil
	}
	s := strings.TrimSpace(string(out))
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("unexpected rev-list output: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// ChangeFile is one row in LogEntry.Changes — a path that landed in
// the commit, with git's standard single-letter status code:
//   - A: added (file present in commit, absent in parent)
//   - M: modified (different blob hash from parent)
//   - D: deleted (file absent in commit, present in parent)
//   - R: renamed (path changed; status carries similarity score from
//     `--name-status -M`, e.g. R100 — clients usually only look at
//     the first byte)
//
// For merge commits the diff is reported against the first parent, so
// changes brought in only by the merge's other parent show up under
// that parent's own commit — same convention git log uses by default.
type ChangeFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// LogEntry describes a single commit. Parents/Refs/Email are populated
// unconditionally. Changes is only populated when Log is called with
// withChanges=true (e.g. via /repos/{name}/log?with_changes=1) and is
// omitted from JSON when nil so the lean shape stays clean for graph
// callers that don't need the disclosure payload.
type LogEntry struct {
	Hash    string       `json:"hash"`
	Parents []string     `json:"parents"`
	Refs    []string     `json:"refs"`
	Author  string       `json:"author"`
	Email   string       `json:"email"`
	Date    string       `json:"date"`
	Message string       `json:"message"`
	Changes []ChangeFile `json:"changes,omitempty"`
}

// fieldSep / recordSep are ASCII control bytes (US / RS) used to fence
// `git log --format` output. Picked over `|` because commit subjects
// and ref decorations both contain `|` in the wild — these bytes do
// not appear in normal git output.
const (
	fieldSep  = "\x1f"
	recordSep = "\x1e"
)

// Log returns recent commits from a repository, including each
// commit's parents, ref decoration, and author email. When
// withChanges is true, every entry also carries its per-path file
// changes (one extra `git diff-tree` call per commit; deliberately
// opt-in so the default graph fetch stays cheap).
func (m *Manager) Log(name string, limit int, withChanges bool) ([]LogEntry, error) {
	path := m.RepoPath(name)
	if !m.Exists(name) {
		return nil, fmt.Errorf("repository %q does not exist", name)
	}

	if limit <= 0 {
		limit = 20
	}

	// Format: hash | parents | refs | author-name | author-email | iso-date | subject
	// Records are %x1e-separated so newlines in subjects don't split rows.
	cmd := exec.Command("git", "-C", path, "log",
		fmt.Sprintf("--max-count=%d", limit),
		"--format=%H"+fieldSep+"%P"+fieldSep+"%D"+fieldSep+"%an"+fieldSep+"%ae"+fieldSep+"%aI"+fieldSep+"%s"+recordSep,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, nil // empty repo, no commits
	}

	var entries []LogEntry
	for _, raw := range strings.Split(string(out), recordSep) {
		raw = strings.Trim(raw, "\n")
		if raw == "" {
			continue
		}
		parts := strings.Split(raw, fieldSep)
		if len(parts) < 7 {
			continue
		}
		e := LogEntry{
			Hash:    parts[0],
			Parents: splitParents(parts[1]),
			Refs:    parseRefs(parts[2]),
			Author:  parts[3],
			Email:   parts[4],
			Date:    parts[5],
			Message: parts[6],
		}
		if withChanges {
			e.Changes = commitChanges(path, e.Hash, len(e.Parents))
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// splitParents splits the space-separated list emitted by %P. An empty
// input (root commit) returns a nil slice rather than [""] so JSON
// serialises as [] and downstream length checks work as expected.
func splitParents(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Split(s, " ")
}

// parseRefs extracts the bare ref names from %D's decoration string.
// Git emits things like:
//
//	HEAD -> master, origin/master, tag: v1.2, origin/HEAD
//
// We strip the "HEAD -> " arrow so HEAD and the branch name both land
// in the slice, drop "tag: " prefixes, and ignore "origin/HEAD"
// symbolic refs (they alias another entry already in the list).
func parseRefs(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var refs []string
	for _, raw := range strings.Split(s, ",") {
		ref := strings.TrimSpace(raw)
		if ref == "" {
			continue
		}
		// "HEAD -> master" → emit both "HEAD" and "master".
		if strings.Contains(ref, " -> ") {
			pair := strings.SplitN(ref, " -> ", 2)
			refs = append(refs, strings.TrimSpace(pair[0]), strings.TrimSpace(pair[1]))
			continue
		}
		// Skip symbolic-ref aliases like "origin/HEAD" — they always
		// duplicate another concrete entry on the same commit.
		if strings.HasSuffix(ref, "/HEAD") {
			continue
		}
		refs = append(refs, strings.TrimPrefix(ref, "tag: "))
	}
	return refs
}

// commitChanges runs `git diff-tree --name-status -r` for one commit
// and returns the resulting [{path, status}] list. For merges the
// implicit base is the first parent (matches `git log` default). On a
// root commit (no parents) we diff against the empty tree so the
// initial set of files surfaces with status A.
func commitChanges(repoPath, hash string, parentCount int) []ChangeFile {
	args := []string{"-C", repoPath, "diff-tree", "--no-commit-id", "--name-status", "-r"}
	if parentCount == 0 {
		// 4b825dc642cb6eb9a060e54bf8d69288fbee4904 is git's well-known
		// empty-tree SHA; works without writing it into the object DB.
		args = append(args, "4b825dc642cb6eb9a060e54bf8d69288fbee4904", hash)
	} else {
		args = append(args, hash)
	}
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil
	}
	var out2 []ChangeFile
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		// Tab-delimited: STATUS\tPATH (or STATUS\tOLD\tNEW for renames).
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		path := parts[1]
		if len(parts) >= 3 {
			path = parts[len(parts)-1] // rename target
		}
		out2 = append(out2, ChangeFile{Status: parts[0], Path: path})
	}
	return out2
}
