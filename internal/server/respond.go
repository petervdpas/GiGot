package server

import (
	"encoding/json"
	"net/http"

	"github.com/petervdpas/GiGot/internal/auth"
	"github.com/petervdpas/GiGot/internal/policy"
)

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorResponse{Error: msg})
}

// requireAllow consults s.policy for the given action/resource and writes a
// 403 if denied. Returns true on allow. Callers should early-return on false.
//
// If the request carries a bearer token, the corresponding TokenEntry is
// stashed in context so repo-scope policies can read the token's allowlist.
func (s *Server) requireAllow(w http.ResponseWriter, r *http.Request, action policy.Action, resource string) bool {
	ctx := r.Context()
	id := auth.IdentityFromContext(ctx)
	if entry := s.tokenStrategy.EntryFromRequest(r); entry != nil {
		ctx = auth.WithTokenEntry(ctx, entry)
	}
	if d := s.policy.Decide(ctx, id, action, resource); !d.Allowed {
		writeError(w, http.StatusForbidden, "forbidden")
		return false
	}
	return true
}
