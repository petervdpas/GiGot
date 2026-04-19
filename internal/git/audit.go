package git

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// AuditRef is the fully-qualified ref name every GiGot audit entry is written
// to. Single main line per repo; clients fetch it like any other ref.
const AuditRef = "refs/audit/main"

const (
	auditEventPath   = "event.json"
	auditAuthorName  = "GiGot Audit"
	auditAuthorEmail = "audit@gigot.local"
	// zeroSHA is git's "ref must not exist" sentinel for update-ref.
	zeroSHA = "0000000000000000000000000000000000000000"
)

// AuditActor identifies the authenticated principal that caused an event.
// Empty means GiGot itself originated the action (scaffold, rotate, etc.).
type AuditActor struct {
	ID       string `json:"id,omitempty"`
	Username string `json:"username,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// AuditEvent is the JSON payload stored in event.json at each audit commit.
// Kept flat so readers can filter with jq without walking structure.
type AuditEvent struct {
	Time  time.Time  `json:"time"`
	Type  string     `json:"type"`
	Actor AuditActor `json:"actor,omitzero"`
	Ref   string     `json:"ref,omitempty"`
	SHA   string     `json:"sha,omitempty"`
	Notes string     `json:"notes,omitempty"`
}

// AuditHead returns the SHA refs/audit/main currently points at, or "" if
// the ref has not been written to yet. Missing ref is not an error — a fresh
// repo simply has no audit history yet.
func (m *Manager) AuditHead(name string) (string, error) {
	if !m.Exists(name) {
		return "", fmt.Errorf("repository %q does not exist", name)
	}
	repoPath := m.RepoPath(name)
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--verify", "--quiet", AuditRef)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// AppendAudit writes one event to refs/audit/main chained on the current
// audit head. Always authored and committed by the GiGot Audit identity so
// a downstream consumer can verify the chain was server-written regardless
// of what actor caused the event. Retries on concurrent contention.
func (m *Manager) AppendAudit(name string, event AuditEvent) (string, error) {
	if !m.Exists(name) {
		return "", fmt.Errorf("repository %q does not exist", name)
	}
	if event.Type == "" {
		return "", fmt.Errorf("audit event type is required")
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}

	repoPath := m.RepoPath(name)

	body, err := json.MarshalIndent(event, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal audit event: %w", err)
	}
	body = append(body, '\n')

	blob, err := hashObject(repoPath, body)
	if err != nil {
		return "", fmt.Errorf("hash-object audit blob: %w", err)
	}

	tree, err := mktreeOneFile(repoPath, auditEventPath, blob)
	if err != nil {
		return "", fmt.Errorf("mktree audit: %w", err)
	}

	id := commitIdentity{
		AuthorName:     auditAuthorName,
		AuthorEmail:    auditAuthorEmail,
		CommitterName:  auditAuthorName,
		CommitterEmail: auditAuthorEmail,
	}
	message := auditCommitMessage(event)

	const maxAttempts = 5
	for range maxAttempts {
		head, err := m.AuditHead(name)
		if err != nil {
			return "", err
		}

		var parents []string
		if head != "" {
			parents = []string{head}
		}

		newCommit, err := commitTree(repoPath, tree, parents, id, message)
		if err != nil {
			return "", fmt.Errorf("commit-tree audit: %w", err)
		}

		if err := updateRefCASAny(repoPath, AuditRef, newCommit, head); err == nil {
			return newCommit, nil
		}
	}
	return "", fmt.Errorf("audit append: gave up after %d contention retries", maxAttempts)
}

// mktreeOneFile builds a tree with exactly one regular-file entry. `git
// mktree` reads `<mode> <type> <sha>\t<path>` from stdin and writes the
// tree — no throwaway index or worktree needed.
func mktreeOneFile(repoPath, path, blob string) (string, error) {
	entry := fmt.Sprintf("100644 blob %s\t%s\n", blob, path)
	cmd := exec.Command("git", "-C", repoPath, "mktree")
	cmd.Stdin = strings.NewReader(entry)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// updateRefCASAny moves any full ref name from expect → next atomically.
// expect == "" is translated to the all-zeros SHA so git enforces
// "ref must not exist yet".
func updateRefCASAny(repoPath, ref, next, expect string) error {
	if expect == "" {
		expect = zeroSHA
	}
	cmd := exec.Command("git", "-C", repoPath, "update-ref", ref, next, expect)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func auditCommitMessage(e AuditEvent) string {
	if e.Notes != "" {
		return fmt.Sprintf("audit: %s (%s)", e.Type, e.Notes)
	}
	return "audit: " + e.Type
}
