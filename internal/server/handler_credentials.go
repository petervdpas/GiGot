package server

import (
	"net/http"
)

// handleCredentialsNames godoc
// @Summary      List credential names + kinds (admin or maintainer)
// @Description  Read-only, non-sensitive listing of credential vault
// @Description  entries. Returns only the human-readable name and kind
// @Description  per row — no secrets, no expiry, no last-used timestamps.
// @Description  The intended caller is a subscriber-side UI wiring a
// @Description  mirror destination (Formidable's mirror form), where
// @Description  the operator needs to know which vault names exist
// @Description  without seeing the secrets the admin holds.
// @Description
// @Description  Gated by role: admin and maintainer accounts pass via
// @Description  either session cookie or bearer token; regular accounts
// @Description  receive 403. The full admin-only metadata view remains
// @Description  at /api/admin/credentials.
// @Tags         credentials
// @Produce      json
// @Success      200  {object}  CredentialNameListResponse
// @Failure      401  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse  "Caller is not admin or maintainer"
// @Failure      405  {object}  ErrorResponse
// @Security     BearerAuth
// @Router       /credentials/names [get]
func (s *Server) handleCredentialsNames(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireMaintainerOrAdmin(w, r) {
		return
	}
	items := s.credentials.All()
	refs := make([]CredentialNameRef, 0, len(items))
	for _, c := range items {
		pv := c.PublicView()
		refs = append(refs, CredentialNameRef{Name: pv.Name, Kind: pv.Kind})
	}
	writeJSON(w, http.StatusOK, CredentialNameListResponse{
		Credentials: refs,
		Count:       len(refs),
	})
}
