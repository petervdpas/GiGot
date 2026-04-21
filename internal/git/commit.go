package git

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Op values accepted in a multi-file commit. Anything else fails validation
// at the top of Commit — the wire contract is defined by design-doc §3.6.
const (
	OpPut    = "put"
	OpDelete = "delete"
)

// Change is one operation in a multi-file commit. Content is required when
// Op == "put" and ignored otherwise.
type Change struct {
	Op      string
	Path    string
	Content []byte
}

// CommitOptions carries the client-supplied pieces of POST /commits plus the
// server-supplied committer identity. Committer stays the scaffolder so git
// log makes the server's role auditable (design §2). SubscriptionUsername
// is appended as a `Subscription-Username:` trailer on every commit the
// manager creates.
type CommitOptions struct {
	ParentVersion        string
	Changes              []Change
	AuthorName           string
	AuthorEmail          string
	CommitterName        string
	CommitterEmail       string
	Message              string
	SubscriptionUsername string
}

// CommitResult describes a successful atomic commit. Shape mirrors
// WriteResult so the wire format is consistent between PUT and POST.
type CommitResult struct {
	Version     string        `json:"version"`
	MergedFrom  string        `json:"merged_from,omitempty"`
	MergedWith  string        `json:"merged_with,omitempty"`
	Changes     []ChangeEntry `json:"changes,omitempty"`
	FastForward bool          `json:"-"`
}

// CommitConflictError carries the full set of per-path conflicts for a
// POST /commits that couldn't be merged. Transactional: any conflict
// aborts the whole commit (design §3.6).
type CommitConflictError struct {
	CurrentVersion string
	Conflicts      []WriteConflict
}

func (e *CommitConflictError) Error() string {
	return fmt.Sprintf("commit conflict on %d path(s)", len(e.Conflicts))
}

// Commit applies a set of put/delete changes atomically against the named
// bare repo (design §3.6, §4). Behaviour mirrors WriteFile but at the set
// level: fast-forward when parent_version == HEAD, 3-way merge via `git
// merge-tree` when parent_version is a strict ancestor, stale-parent
// CommitConflictError when it isn't, and CommitConflictError listing every
// conflicting path when the merge hits conflicts. Never partial: on any
// error the HEAD ref is untouched.
func (m *Manager) Commit(name string, opts CommitOptions) (CommitResult, error) {
	if !m.Exists(name) {
		return CommitResult{}, fmt.Errorf("repository %q does not exist", name)
	}
	if len(opts.Changes) == 0 {
		return CommitResult{}, fmt.Errorf("changes is required")
	}
	if opts.ParentVersion == "" {
		return CommitResult{}, fmt.Errorf("parent_version is required")
	}
	if opts.AuthorName == "" || opts.AuthorEmail == "" {
		return CommitResult{}, fmt.Errorf("author name and email are required")
	}
	if opts.CommitterName == "" || opts.CommitterEmail == "" {
		return CommitResult{}, fmt.Errorf("committer name and email are required")
	}
	for i, c := range opts.Changes {
		if c.Path == "" {
			return CommitResult{}, fmt.Errorf("changes[%d]: path is required", i)
		}
		if c.Op != OpPut && c.Op != OpDelete {
			return CommitResult{}, fmt.Errorf("changes[%d]: invalid op %q", i, c.Op)
		}
	}
	if opts.Message == "" {
		opts.Message = fmt.Sprintf("Apply %d change(s)", len(opts.Changes))
	}

	repoPath := m.RepoPath(name)

	// Probe HEAD before resolving parent so empty repos surface as
	// ErrRepoEmpty instead of masking as ErrVersionNotFound.
	if _, err := m.Head(name); err != nil {
		return CommitResult{}, err
	}

	parent, err := resolveCommit(repoPath, opts.ParentVersion)
	if err != nil {
		return CommitResult{}, ErrVersionNotFound
	}

	clientTree, err := treeWithChanges(repoPath, parent, opts.Changes)
	if err != nil {
		return CommitResult{}, err
	}

	clientMessage := withSubscriptionTrailer(opts.Message, opts.SubscriptionUsername)
	clientCommit, err := commitTree(repoPath, clientTree, []string{parent}, commitIdentity{
		AuthorName:     opts.AuthorName,
		AuthorEmail:    opts.AuthorEmail,
		CommitterName:  opts.CommitterName,
		CommitterEmail: opts.CommitterEmail,
	}, clientMessage)
	if err != nil {
		return CommitResult{}, fmt.Errorf("client commit-tree: %w", err)
	}

	const maxAttempts = 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		head, err := m.Head(name)
		if err != nil {
			return CommitResult{}, err
		}

		if parent == head.Version {
			if err := updateRefCAS(repoPath, head.DefaultBranch, clientCommit, head.Version); err == nil {
				changes, _ := diffTreeChanges(repoPath, parent, clientCommit)
				return CommitResult{
					Version:     clientCommit,
					FastForward: true,
					Changes:     changes,
				}, nil
			}
			continue
		}

		if !isAncestor(repoPath, parent, head.Version) {
			return CommitResult{}, &CommitConflictError{
				CurrentVersion: head.Version,
				Conflicts:      staleParentConflicts(head.Version, opts.Changes),
			}
		}

		mergedTree, clean, conflictPaths, err := mergeTree(repoPath, head.Version, clientCommit)
		if err != nil {
			return CommitResult{}, fmt.Errorf("merge-tree: %w", err)
		}
		if !clean {
			return CommitResult{}, &CommitConflictError{
				CurrentVersion: head.Version,
				Conflicts:      mergeConflicts(repoPath, parent, head.Version, clientCommit, conflictPaths),
			}
		}

		mergeMsg := withSubscriptionTrailer(
			fmt.Sprintf("Merge client commit (%d change(s))", len(opts.Changes)),
			opts.SubscriptionUsername)
		mergeCommit, err := commitTree(repoPath, mergedTree, []string{head.Version, clientCommit}, commitIdentity{
			AuthorName:     opts.CommitterName,
			AuthorEmail:    opts.CommitterEmail,
			CommitterName:  opts.CommitterName,
			CommitterEmail: opts.CommitterEmail,
		}, mergeMsg)
		if err != nil {
			return CommitResult{}, fmt.Errorf("merge commit-tree: %w", err)
		}
		if err := updateRefCAS(repoPath, head.DefaultBranch, mergeCommit, head.Version); err == nil {
			// Diff against HEAD, not our client commit — the merge
			// reconciles both sides, and the client ledger needs to
			// know what landed on the branch (authoritative post-merge
			// blob SHAs) rather than what we originally sent.
			changes, _ := diffTreeChanges(repoPath, head.Version, mergeCommit)
			return CommitResult{
				Version:    mergeCommit,
				MergedFrom: parent,
				MergedWith: head.Version,
				Changes:    changes,
			}, nil
		}
	}
	return CommitResult{}, fmt.Errorf("commit: gave up after %d contention retries", maxAttempts)
}

// treeWithChanges builds a tree from baseCommit with every Change applied in
// order. Uses a throwaway GIT_INDEX_FILE so the bare repo's index (if any)
// is not touched. Paths rejected by git (traversal, ".git/...") surface as
// ErrInvalidPath regardless of which change triggered them.
func treeWithChanges(repoPath, baseCommit string, changes []Change) (string, error) {
	tmpIndex, err := os.CreateTemp("", "gigot-index-*")
	if err != nil {
		return "", err
	}
	tmpIndex.Close()
	defer os.Remove(tmpIndex.Name())

	// update-index --force-remove refuses to run in a bare repo ("this
	// operation must be run in a work tree"). Hand it a throwaway work
	// tree so the delete path works; put/add don't need it but inherit
	// the env anyway.
	tmpWork, err := os.MkdirTemp("", "gigot-work-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpWork)

	env := append(os.Environ(),
		"GIT_INDEX_FILE="+tmpIndex.Name(),
		"GIT_WORK_TREE="+tmpWork,
	)

	readTree := exec.Command("git", "-C", repoPath, "read-tree", baseCommit)
	readTree.Env = env
	if out, err := readTree.CombinedOutput(); err != nil {
		return "", fmt.Errorf("read-tree %s: %s: %w", baseCommit, string(out), err)
	}

	for i, c := range changes {
		switch c.Op {
		case OpPut:
			blobSHA, err := hashObject(repoPath, c.Content)
			if err != nil {
				return "", fmt.Errorf("changes[%d] hash-object: %w", i, err)
			}
			upd := exec.Command("git", "-C", repoPath, "update-index", "--add",
				"--cacheinfo", "100644,"+blobSHA+","+filepath.ToSlash(c.Path))
			upd.Env = env
			if out, err := upd.CombinedOutput(); err != nil {
				if strings.Contains(string(out), "Invalid path") {
					return "", ErrInvalidPath
				}
				return "", fmt.Errorf("changes[%d] update-index: %s: %w", i, string(out), err)
			}
		case OpDelete:
			// --force-remove makes "delete an absent path" a no-op rather
			// than an error; merge-time conflict detection still catches
			// the interesting cases (delete-vs-modify, etc.).
			upd := exec.Command("git", "-C", repoPath, "update-index", "--force-remove",
				filepath.ToSlash(c.Path))
			upd.Env = env
			if out, err := upd.CombinedOutput(); err != nil {
				if strings.Contains(string(out), "Invalid path") {
					return "", ErrInvalidPath
				}
				return "", fmt.Errorf("changes[%d] remove: %s: %w", i, string(out), err)
			}
		}
	}

	writeTree := exec.Command("git", "-C", repoPath, "write-tree")
	writeTree.Env = env
	out, err := writeTree.Output()
	if err != nil {
		return "", fmt.Errorf("write-tree: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// staleParentConflicts builds the 409 body for the not-an-ancestor case —
// every client change is echoed back with only yours_b64 populated, since
// the server didn't attempt a merge.
func staleParentConflicts(currentVersion string, changes []Change) []WriteConflict {
	out := make([]WriteConflict, 0, len(changes))
	for _, c := range changes {
		wc := WriteConflict{
			CurrentVersion: currentVersion,
			Path:           c.Path,
		}
		if c.Op == OpPut {
			wc.YoursB64 = base64.StdEncoding.EncodeToString(c.Content)
		}
		// For a delete, "yours" is absence — leave YoursB64 empty.
		out = append(out, wc)
	}
	return out
}

// mergeConflicts materialises the 409 body for a real merge conflict: for
// each conflicting path, fetch base (at parent), theirs (at head), and
// yours (at clientCommit). A missing blob at any side is expressed as an
// empty string, matching the add/add and delete/modify shapes in §3.5.
func mergeConflicts(repoPath, parent, head, clientCommit string, paths []string) []WriteConflict {
	out := make([]WriteConflict, 0, len(paths))
	for _, p := range paths {
		base, _ := catBlob(repoPath, parent, p)
		theirs, _ := catBlob(repoPath, head, p)
		yours, _ := catBlob(repoPath, clientCommit, p)
		out = append(out, WriteConflict{
			CurrentVersion: head,
			Path:           p,
			BaseB64:        maybeB64(base),
			TheirsB64:      maybeB64(theirs),
			YoursB64:       maybeB64(yours),
		})
	}
	return out
}
