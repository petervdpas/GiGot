package server

import (
	"net/http"

	"github.com/petervdpas/GiGot/internal/accounts"
)

// MeResponse is the body of GET /api/me — the self-serve equivalent
// of /admin/session for signed-in regular users. Lists the caller's
// own account profile and subscription keys, so a teammate can copy
// their key into a Formidable client without an admin handing it out
// of band. Admins see the same payload from their own perspective;
// no provider-wide fan-out, only the caller's rows.
type MeResponse struct {
	Username      string          `json:"username"`
	Provider      string          `json:"provider"`
	DisplayName   string          `json:"display_name,omitempty"`
	Email         string          `json:"email,omitempty"`
	Role          string          `json:"role"`
	Subscriptions []TokenListItem `json:"subscriptions"`
}

// handleMe godoc
// @Summary      Current user profile and subscription keys
// @Description  Returns the signed-in caller's account row plus the
// @Description  subscription keys bound to it. Requires a valid
// @Description  session cookie; NOT admin-gated — any authenticated
// @Description  user reaches their own profile.
// @Tags         me
// @Produce      json
// @Success      200  {object}  MeResponse
// @Failure      401  {object}  ErrorResponse
// @Router       /me [get]
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := s.requireSession(w, r)
	if id == nil {
		return
	}

	resp := MeResponse{
		Username:      id.Username,
		Provider:      id.AccountProvider,
		Subscriptions: []TokenListItem{},
	}
	if acc, err := s.accounts.Get(id.AccountProvider, id.Username); err == nil {
		resp.DisplayName = acc.DisplayName
		resp.Email = acc.Email
		resp.Role = acc.Role
	}

	// Walk tokens once, keep only those whose stored Username parses to
	// the caller's (account_provider, identifier). The token.Username
	// can be either "provider:identifier" or a bare string that
	// resolves to (local, string); reuse parseTokenUsername so the
	// matching rules stay aligned with /api/auth/token's acceptance.
	for _, tok := range s.tokenStrategy.List() {
		prov, ident, err := parseTokenUsername(tok.Username)
		if err != nil {
			continue
		}
		if prov != id.AccountProvider || ident != id.Username {
			continue
		}
		resp.Subscriptions = append(resp.Subscriptions, TokenListItem{
			Token:      tok.Token,
			Username:   tok.Username,
			Repo:       tok.Repo,
			Abilities:  tok.Abilities,
			HasAccount: s.accounts.Has(prov, ident),
		})
	}

	// Ensure Role is populated even if the account row was deleted
	// mid-session — a signed-in user with no account row is effectively
	// a regular with no access; the UI should render gracefully.
	if resp.Role == "" {
		resp.Role = accounts.RoleRegular
	}

	writeJSON(w, http.StatusOK, resp)
}
