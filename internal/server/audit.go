package server

import (
	"log"
	"net/http"

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
