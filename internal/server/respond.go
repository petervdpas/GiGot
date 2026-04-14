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
func (s *Server) requireAllow(w http.ResponseWriter, r *http.Request, action policy.Action, resource string) bool {
	id := auth.IdentityFromContext(r.Context())
	if d := s.policy.Decide(id, action, resource); !d.Allowed {
		writeError(w, http.StatusForbidden, "forbidden")
		return false
	}
	return true
}
