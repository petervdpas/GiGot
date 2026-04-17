package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// ChangeOp values returned in a ChangeEntry.Op. Mirrors the wire contract
// defined in docs/design/structured-sync-api.md §3.7.
const (
	ChangeAdded    = "added"
	ChangeModified = "modified"
	ChangeDeleted  = "deleted"
)

// ChangeEntry describes one path that differs between two versions. Blob is
// the post-change blob SHA for added/modified and the pre-change SHA for
// deleted, so a client always has *something* concrete to fetch if it wants
// to reconstruct history.
type ChangeEntry struct {
	Path string `json:"path"`
	Op   string `json:"op"`
	Blob string `json:"blob"`
}

// ChangesInfo is the response body of GET /changes?since=<sha>.
type ChangesInfo struct {
	From    string        `json:"from"`
	To      string        `json:"to"`
	Changes []ChangeEntry `json:"changes"`
}

// Changes returns the set of paths that differ between `since` and current
// HEAD. `since` must be a strict ancestor of HEAD (or equal to it); anything
// else surfaces as ErrStaleParent so the client knows to re-fetch a full
// snapshot instead of applying a misleading diff. Empty repo ⇒ ErrRepoEmpty,
// unresolvable `since` ⇒ ErrVersionNotFound.
func (m *Manager) Changes(name, since string) (ChangesInfo, error) {
	if !m.Exists(name) {
		return ChangesInfo{}, fmt.Errorf("repository %q does not exist", name)
	}
	if since == "" {
		return ChangesInfo{}, fmt.Errorf("since is required")
	}

	head, err := m.Head(name)
	if err != nil {
		return ChangesInfo{}, err
	}

	repoPath := m.RepoPath(name)
	from, err := resolveCommit(repoPath, since)
	if err != nil {
		return ChangesInfo{}, ErrVersionNotFound
	}

	if from == head.Version {
		return ChangesInfo{From: from, To: head.Version, Changes: []ChangeEntry{}}, nil
	}

	if !isAncestor(repoPath, from, head.Version) {
		return ChangesInfo{}, ErrStaleParent
	}

	// diff-tree --raw -z yields NUL-separated records of the form
	//   ":<old-mode> <new-mode> <old-sha> <new-sha> <status>\0<path>\0"
	// which is unambiguous even for paths with spaces or newlines.
	cmd := exec.Command("git", "-C", repoPath, "diff-tree",
		"--raw", "-r", "-z", "--no-commit-id", from, head.Version)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return ChangesInfo{}, fmt.Errorf("diff-tree: %s: %w", strings.TrimSpace(stderr.String()), err)
	}

	entries, err := parseDiffTreeZ(stdout.Bytes())
	if err != nil {
		return ChangesInfo{}, err
	}
	return ChangesInfo{From: from, To: head.Version, Changes: entries}, nil
}

// parseDiffTreeZ walks the NUL-separated output of `git diff-tree --raw -z`.
// Record shape per path:
//
//	":<old-mode> <new-mode> <old-sha> <new-sha> <status>\0<path>\0"
//
// For rename/copy statuses (R100, C75, ...) git emits a second path field,
// but we don't pass -M/-C so those never appear — only A, M, D.
func parseDiffTreeZ(raw []byte) ([]ChangeEntry, error) {
	if len(raw) == 0 {
		return []ChangeEntry{}, nil
	}
	fields := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	out := []ChangeEntry{}
	for i := 0; i < len(fields); {
		header := fields[i]
		if header == "" {
			i++
			continue
		}
		if !strings.HasPrefix(header, ":") {
			return nil, fmt.Errorf("diff-tree: unexpected record %q", header)
		}
		parts := strings.Fields(strings.TrimPrefix(header, ":"))
		if len(parts) < 5 {
			return nil, fmt.Errorf("diff-tree: malformed header %q", header)
		}
		oldSHA, newSHA, status := parts[2], parts[3], parts[4]

		if i+1 >= len(fields) {
			return nil, fmt.Errorf("diff-tree: header %q missing path", header)
		}
		path := fields[i+1]
		i += 2

		entry := ChangeEntry{Path: path}
		switch {
		case strings.HasPrefix(status, "A"):
			entry.Op, entry.Blob = ChangeAdded, newSHA
		case strings.HasPrefix(status, "D"):
			entry.Op, entry.Blob = ChangeDeleted, oldSHA
		case strings.HasPrefix(status, "M"), strings.HasPrefix(status, "T"):
			entry.Op, entry.Blob = ChangeModified, newSHA
		default:
			return nil, fmt.Errorf("diff-tree: unsupported status %q on %q", status, path)
		}
		out = append(out, entry)
	}
	return out, nil
}
