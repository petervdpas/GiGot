package git

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrStaleParent is returned when a client-supplied parent_version is not an
// ancestor of current HEAD — forked history, rewritten base, or garbage.
// The write never attempts a merge in this case; the client must re-fetch.
var ErrStaleParent = errors.New("parent version is not an ancestor of current head")

// ErrInvalidPath is returned when the target path is rejected by git
// (traversal outside the repo, absolute paths, ".."). Git's own
// update-index catches these, which is plenty for security; we surface a
// sentinel so the HTTP layer can map it to 400 instead of 500.
var ErrInvalidPath = errors.New("path is not valid inside the repository")

// WriteConflict carries the blob triple for a real merge conflict on a single
// path. Any of BaseB64/TheirsB64/YoursB64 may be empty to express add/add or
// delete/modify shapes.
type WriteConflict struct {
	CurrentVersion string `json:"current_version"`
	Path           string `json:"path"`
	BaseB64        string `json:"base_b64,omitempty"`
	TheirsB64      string `json:"theirs_b64,omitempty"`
	YoursB64       string `json:"yours_b64"`
}

// WriteConflictError wraps a WriteConflict so the merge outcome can flow
// through a regular error return. Handlers unwrap it to render 409 bodies.
type WriteConflictError struct {
	Conflict WriteConflict
}

func (e *WriteConflictError) Error() string {
	return "write conflict on " + e.Conflict.Path
}

// WriteResult describes a successful single-file write.
type WriteResult struct {
	Version     string `json:"version"`
	MergedFrom  string `json:"merged_from,omitempty"`
	MergedWith  string `json:"merged_with,omitempty"`
	FastForward bool   `json:"-"`
}

// WriteOptions carries the client-supplied pieces of a PUT /files/{path}
// plus the server-supplied committer identity. Committer stays the
// scaffolder (or equivalent server identity) so git log makes the server's
// role auditable even when authors are forged (design §2 authoring identity).
// SubscriptionUsername, when non-empty, is appended as a
// `Subscription-Username:` trailer on every commit the manager creates
// (fast-forward, client, and merge) so audit survives a forged author.
type WriteOptions struct {
	ParentVersion        string
	Path                 string
	Content              []byte
	AuthorName           string
	AuthorEmail          string
	CommitterName        string
	CommitterEmail       string
	Message              string
	SubscriptionUsername string
}

// WriteFile performs a single-file write against the named bare repo,
// implementing the design-doc flow for PUT /files/{path} (§3.5, §4):
// fast-forward when parent_version == HEAD, 3-way merge via `git merge-tree`
// when parent_version is a strict ancestor of HEAD, ErrStaleParent when it
// isn't, and WriteConflictError when the merge hits a real conflict on the
// target path.
func (m *Manager) WriteFile(name string, opts WriteOptions) (WriteResult, error) {
	if !m.Exists(name) {
		return WriteResult{}, fmt.Errorf("repository %q does not exist", name)
	}
	if opts.Path == "" {
		return WriteResult{}, fmt.Errorf("path is required")
	}
	if opts.ParentVersion == "" {
		return WriteResult{}, fmt.Errorf("parent_version is required")
	}
	if opts.AuthorName == "" || opts.AuthorEmail == "" {
		return WriteResult{}, fmt.Errorf("author name and email are required")
	}
	if opts.CommitterName == "" || opts.CommitterEmail == "" {
		return WriteResult{}, fmt.Errorf("committer name and email are required")
	}
	if opts.Message == "" {
		opts.Message = "Update " + opts.Path
	}

	repoPath := m.RepoPath(name)

	// Probe HEAD once up front so an empty repo surfaces as ErrRepoEmpty
	// before we try to resolve parent_version (which would otherwise fail
	// as ErrVersionNotFound and mask the real cause).
	if _, err := m.Head(name); err != nil {
		return WriteResult{}, err
	}

	parent, err := resolveCommit(repoPath, opts.ParentVersion)
	if err != nil {
		return WriteResult{}, ErrVersionNotFound
	}

	blobSHA, err := hashObject(repoPath, opts.Content)
	if err != nil {
		return WriteResult{}, fmt.Errorf("hash-object: %w", err)
	}

	clientTree, err := treeWithFile(repoPath, parent, opts.Path, blobSHA)
	if err != nil {
		return WriteResult{}, err
	}

	clientMessage := withSubscriptionTrailer(opts.Message, opts.SubscriptionUsername)
	clientCommit, err := commitTree(repoPath, clientTree, []string{parent}, commitIdentity{
		AuthorName:     opts.AuthorName,
		AuthorEmail:    opts.AuthorEmail,
		CommitterName:  opts.CommitterName,
		CommitterEmail: opts.CommitterEmail,
	}, clientMessage)
	if err != nil {
		return WriteResult{}, fmt.Errorf("client commit-tree: %w", err)
	}

	// The race window is between reading HEAD and winning the CAS — if a
	// concurrent writer advances HEAD first, update-ref fails and we must
	// re-evaluate (a fast-forward may now need to become a merge). Cap the
	// loop so a pathological contention storm surfaces as an error rather
	// than spinning forever.
	const maxAttempts = 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		head, err := m.Head(name)
		if err != nil {
			return WriteResult{}, err
		}

		if parent == head.Version {
			err := updateRefCAS(repoPath, head.DefaultBranch, clientCommit, head.Version)
			if err == nil {
				return WriteResult{Version: clientCommit, FastForward: true}, nil
			}
			continue // lost the CAS race — someone advanced HEAD
		}

		if !isAncestor(repoPath, parent, head.Version) {
			return WriteResult{}, &WriteConflictError{Conflict: WriteConflict{
				CurrentVersion: head.Version,
				Path:           opts.Path,
				YoursB64:       base64.StdEncoding.EncodeToString(opts.Content),
			}}
		}

		mergedTree, clean, _, err := mergeTree(repoPath, head.Version, clientCommit)
		if err != nil {
			return WriteResult{}, fmt.Errorf("merge-tree: %w", err)
		}
		if !clean {
			base, _ := catBlob(repoPath, parent, opts.Path)
			theirs, _ := catBlob(repoPath, head.Version, opts.Path)
			return WriteResult{}, &WriteConflictError{Conflict: WriteConflict{
				CurrentVersion: head.Version,
				Path:           opts.Path,
				BaseB64:        maybeB64(base),
				TheirsB64:      maybeB64(theirs),
				YoursB64:       base64.StdEncoding.EncodeToString(opts.Content),
			}}
		}

		// Server-authored merge commit: both author and committer are the
		// scaffolder identity (design §2). The subscription username still
		// goes in a trailer so audit survives a forged client author on the
		// underlying client commit.
		mergeMsg := withSubscriptionTrailer(fmt.Sprintf("Merge client change to %s", opts.Path), opts.SubscriptionUsername)
		mergeCommit, err := commitTree(repoPath, mergedTree, []string{head.Version, clientCommit}, commitIdentity{
			AuthorName:     opts.CommitterName,
			AuthorEmail:    opts.CommitterEmail,
			CommitterName:  opts.CommitterName,
			CommitterEmail: opts.CommitterEmail,
		}, mergeMsg)
		if err != nil {
			return WriteResult{}, fmt.Errorf("merge commit-tree: %w", err)
		}
		if err := updateRefCAS(repoPath, head.DefaultBranch, mergeCommit, head.Version); err == nil {
			return WriteResult{
				Version:    mergeCommit,
				MergedFrom: parent,
				MergedWith: head.Version,
			}, nil
		}
		// CAS lost again — loop and re-evaluate against the new HEAD.
	}
	return WriteResult{}, fmt.Errorf("write: gave up after %d contention retries", maxAttempts)
}

// resolveCommit returns the 40-char SHA for rev or an error if it does not
// resolve to a commit. Callers map that error to ErrVersionNotFound.
func resolveCommit(repoPath, rev string) (string, error) {
	out, err := exec.Command("git", "-C", repoPath, "rev-parse", "--verify", rev+"^{commit}").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// hashObject writes a blob to the object store via `git hash-object -w --stdin`.
func hashObject(repoPath string, content []byte) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "hash-object", "-w", "--stdin")
	cmd.Stdin = bytes.NewReader(content)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// treeWithFile returns a tree SHA representing baseCommit's tree with one
// blob at path replaced by blobSHA (or added if absent). Uses a throwaway
// GIT_INDEX_FILE so the bare repo's index (if any) is not touched.
func treeWithFile(repoPath, baseCommit, path, blobSHA string) (string, error) {
	tmpIndex, err := os.CreateTemp("", "gigot-index-*")
	if err != nil {
		return "", err
	}
	tmpIndex.Close()
	defer os.Remove(tmpIndex.Name())

	env := append(os.Environ(), "GIT_INDEX_FILE="+tmpIndex.Name())

	readTree := exec.Command("git", "-C", repoPath, "read-tree", baseCommit)
	readTree.Env = env
	if out, err := readTree.CombinedOutput(); err != nil {
		return "", fmt.Errorf("read-tree %s: %s: %w", baseCommit, string(out), err)
	}

	upd := exec.Command("git", "-C", repoPath, "update-index", "--add",
		"--cacheinfo", "100644,"+blobSHA+","+filepath.ToSlash(path))
	upd.Env = env
	if out, err := upd.CombinedOutput(); err != nil {
		if strings.Contains(string(out), "Invalid path") {
			return "", ErrInvalidPath
		}
		return "", fmt.Errorf("update-index: %s: %w", string(out), err)
	}

	writeTree := exec.Command("git", "-C", repoPath, "write-tree")
	writeTree.Env = env
	out, err := writeTree.Output()
	if err != nil {
		return "", fmt.Errorf("write-tree: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// commitIdentity is the author/committer pair for one commit-tree call.
// Split from WriteOptions because merge commits use a different author
// (scaffolder) than client commits (client-supplied), and threading a second
// WriteOptions through just for this would be noisier than the split.
type commitIdentity struct {
	AuthorName     string
	AuthorEmail    string
	CommitterName  string
	CommitterEmail string
}

// commitTree creates a commit object with the given tree and parents,
// stamping author and committer identities separately.
func commitTree(repoPath, tree string, parents []string, id commitIdentity, message string) (string, error) {
	args := []string{"-C", repoPath, "commit-tree", tree}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	args = append(args, "-m", message)

	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME="+id.AuthorName,
		"GIT_AUTHOR_EMAIL="+id.AuthorEmail,
		"GIT_COMMITTER_NAME="+id.CommitterName,
		"GIT_COMMITTER_EMAIL="+id.CommitterEmail,
	)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// withSubscriptionTrailer appends a `Subscription-Username:` trailer to a
// commit message. Preserves audit context regardless of what author the
// client supplied; the trailer is what survives a forged/compromised client
// (design §6 Phase 2). When username is empty (e.g. auth disabled in dev),
// the message is returned unchanged.
func withSubscriptionTrailer(msg, username string) string {
	if username == "" {
		return msg
	}
	return strings.TrimRight(msg, "\n") + "\n\nSubscription-Username: " + username + "\n"
}

// updateRefCAS moves refs/heads/<branch> from expect → next, failing if the
// ref has moved concurrently. This is how we stay race-safe without a lock.
func updateRefCAS(repoPath, branch, next, expect string) error {
	cmd := exec.Command("git", "-C", repoPath, "update-ref", "refs/heads/"+branch, next, expect)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// isAncestor reports whether possibleAncestor is an ancestor of descendant.
// merge-base --is-ancestor exits 0 for yes, 1 for no.
func isAncestor(repoPath, possibleAncestor, descendant string) bool {
	err := exec.Command("git", "-C", repoPath, "merge-base", "--is-ancestor", possibleAncestor, descendant).Run()
	return err == nil
}

// mergeTree runs `git merge-tree --write-tree --name-only theirs yours` and
// returns the resulting tree SHA, a clean flag, and the list of conflicting
// paths (empty on clean merges). The command exits 0 on a clean merge and 1
// on conflict; we distinguish those via exit status. Genuine failures (bad
// revs, IO errors) surface as the error return.
//
// --name-only dedupes conflicts across stages (base/ours/theirs), so a
// single conflicted file appears exactly once. That matches the 409 shape
// for both PUT (single path, len == 1) and POST /commits (multi-path).
func mergeTree(repoPath, theirs, yours string) (string, bool, []string, error) {
	cmd := exec.Command("git", "-C", repoPath, "merge-tree", "--write-tree", "--name-only", "--no-messages", theirs, yours)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return "", false, nil, fmt.Errorf("merge-tree: empty output (stderr=%s)", strings.TrimSpace(stderr.String()))
	}
	tree := lines[0]
	if err == nil {
		return tree, true, nil, nil
	}
	// Exit status 1 means conflict; anything else is a real error.
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		var paths []string
		for _, p := range lines[1:] {
			if p != "" {
				paths = append(paths, p)
			}
		}
		return tree, false, paths, nil
	}
	return "", false, nil, fmt.Errorf("merge-tree: %s: %w", strings.TrimSpace(stderr.String()), err)
}

// catBlob returns the bytes of <rev>:<path> or ErrPathNotFound if absent.
func catBlob(repoPath, rev, path string) ([]byte, error) {
	out, err := exec.Command("git", "-C", repoPath, "cat-file", "blob", rev+":"+path).Output()
	if err != nil {
		return nil, ErrPathNotFound
	}
	return out, nil
}

func maybeB64(b []byte) string {
	if b == nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b)
}
