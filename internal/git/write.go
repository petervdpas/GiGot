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
type WriteOptions struct {
	ParentVersion  string
	Path           string
	Content        []byte
	AuthorName     string
	AuthorEmail    string
	CommitterName  string
	CommitterEmail string
	Message        string
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

	head, err := m.Head(name)
	if err != nil {
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
		return WriteResult{}, fmt.Errorf("build client tree: %w", err)
	}

	clientCommit, err := commitTree(repoPath, clientTree, []string{parent}, opts, opts.Message)
	if err != nil {
		return WriteResult{}, fmt.Errorf("client commit-tree: %w", err)
	}

	if parent == head.Version {
		if err := updateRefCAS(repoPath, head.DefaultBranch, clientCommit, head.Version); err != nil {
			return WriteResult{}, fmt.Errorf("fast-forward update-ref: %w", err)
		}
		return WriteResult{Version: clientCommit, FastForward: true}, nil
	}

	if !isAncestor(repoPath, parent, head.Version) {
		return WriteResult{}, &WriteConflictError{Conflict: WriteConflict{
			CurrentVersion: head.Version,
			Path:           opts.Path,
			YoursB64:       base64.StdEncoding.EncodeToString(opts.Content),
		}}
	}

	mergedTree, clean, err := mergeTree(repoPath, head.Version, clientCommit)
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

	mergeMsg := fmt.Sprintf("Merge client change to %s", opts.Path)
	mergeCommit, err := commitTree(repoPath, mergedTree, []string{head.Version, clientCommit}, opts, mergeMsg)
	if err != nil {
		return WriteResult{}, fmt.Errorf("merge commit-tree: %w", err)
	}
	if err := updateRefCAS(repoPath, head.DefaultBranch, mergeCommit, head.Version); err != nil {
		return WriteResult{}, fmt.Errorf("merge update-ref: %w", err)
	}
	return WriteResult{
		Version:    mergeCommit,
		MergedFrom: parent,
		MergedWith: head.Version,
	}, nil
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

// commitTree creates a commit object with the given tree and parents,
// stamping author and committer identities separately per opts.
func commitTree(repoPath, tree string, parents []string, opts WriteOptions, message string) (string, error) {
	args := []string{"-C", repoPath, "commit-tree", tree}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	args = append(args, "-m", message)

	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME="+opts.AuthorName,
		"GIT_AUTHOR_EMAIL="+opts.AuthorEmail,
		"GIT_COMMITTER_NAME="+opts.CommitterName,
		"GIT_COMMITTER_EMAIL="+opts.CommitterEmail,
	)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
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

// mergeTree runs `git merge-tree --write-tree theirs yours` and returns the
// resulting tree SHA plus a clean flag. The command exits 0 on a clean merge
// and non-zero on conflict; we distinguish those via exit status. Genuine
// failures (bad revs, IO errors) surface as the third return.
func mergeTree(repoPath, theirs, yours string) (string, bool, error) {
	cmd := exec.Command("git", "-C", repoPath, "merge-tree", "--write-tree", theirs, yours)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	lines := strings.SplitN(strings.TrimRight(stdout.String(), "\n"), "\n", 2)
	if len(lines) == 0 || lines[0] == "" {
		return "", false, fmt.Errorf("merge-tree: empty output (stderr=%s)", strings.TrimSpace(stderr.String()))
	}
	tree := lines[0]
	if err == nil {
		return tree, true, nil
	}
	// Exit status 1 means conflict; anything else is a real error.
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		return tree, false, nil
	}
	return "", false, fmt.Errorf("merge-tree: %s: %w", strings.TrimSpace(stderr.String()), err)
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
