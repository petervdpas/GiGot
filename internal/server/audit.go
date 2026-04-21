package server

import (
	"log"
	"net/http"
	"time"

	"github.com/petervdpas/GiGot/internal/auth"
	gitmanager "github.com/petervdpas/GiGot/internal/git"
)

// Audit event types. Centralised here so a typo in a handler can't drift the
// schema that clients read.
const (
	AuditTypeRepoCreate             = "repo_create"
	AuditTypeFilePut                = "file_put"
	AuditTypeCommit                 = "commit"
	AuditTypePushReceived           = "push_received"
	AuditTypeRepoConvertFormidable  = "repo_convert_formidable"
)

// auditActor extracts the authenticated principal from the request context
// and shapes it for inclusion in an audit event. A request that arrived
// without an auth identity (e.g. auth disabled in dev) yields the zero
// actor — still a valid event, just unattributed.
func auditActor(r *http.Request) gitmanager.AuditActor {
	id := auth.IdentityFromContext(r.Context())
	if id == nil {
		return gitmanager.AuditActor{}
	}
	return gitmanager.AuditActor{
		ID:       id.ID,
		Username: id.Username,
		Provider: id.Provider,
	}
}

// appendAudit writes one entry to refs/audit/main on the named repo, never
// failing the caller's operation on audit error. Audit is observability for
// a repo that already took its user-facing write — losing an entry is worth
// logging, not worth propagating a 500 to the client.
func (s *Server) appendAudit(name string, event gitmanager.AuditEvent) {
	if _, err := s.git.AppendAudit(name, event); err != nil {
		log.Printf("audit: append failed on repo %q (type=%s): %v", name, event.Type, err)
	}
}

// autofixFormidableGitignore is the post-write self-heal hook for
// Formidable-first repos. Runs ensureFormidableGitignore (narrow to
// the .gitignore file only) and audits the new commit on success.
// Returns true when a fix commit actually landed so the caller can
// decide whether to also enqueue the mirror worker — handlers that
// aren't themselves triggering a push (POST /commits, PUT /files)
// want that enqueue, while handlers that are already pushing
// (Sync-now) don't, because syncOnce will carry the new commit to
// the mirror anyway. All errors are logged, never surfaced — the
// user's original write has already succeeded by the time this runs.
func (s *Server) autofixFormidableGitignore(r *http.Request, name string) bool {
	newVersion, err := ensureFormidableGitignore(s.git, name, time.Now())
	if err != nil {
		log.Printf("autofix gitignore: repo %q: %v", name, err)
		return false
	}
	if newVersion == "" {
		return false
	}
	s.appendAudit(name, gitmanager.AuditEvent{
		Type:  AuditTypeFilePut,
		Actor: auditActor(r),
		SHA:   newVersion,
		Notes: gitignorePath + " (autofix)",
	})
	return true
}
