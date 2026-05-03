package server

import (
	"net/http"

	"github.com/petervdpas/GiGot/internal/accounts"
	"github.com/petervdpas/GiGot/internal/auth"
	"github.com/petervdpas/GiGot/internal/tags"
)

// MeResponse is the body of GET /api/me — the self-serve equivalent
// of /admin/session for signed-in regular users. Lists the caller's
// own account profile and subscription keys, so a teammate can copy
// their key into a Formidable client without an admin handing it out
// of band. Admins see the same payload from their own perspective;
// no provider-wide fan-out, only the caller's rows.
//
// Two auth modes accepted:
//
//   - Session cookie — full profile + every subscription bound to
//     the caller's account.
//   - Bearer token — same shape, but Subscriptions contains only the
//     single token presented (the bearer can't enumerate sibling
//     keys it doesn't already hold). Lets API clients introspect
//     their own role + abilities without probing 403s.
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
// @Description  Returns the caller's account row plus subscription
// @Description  keys. Accepts either a session cookie (returns every
// @Description  subscription bound to the account) or a bearer token
// @Description  (returns the single subscription that token represents).
// @Tags         me
// @Produce      json
// @Success      200  {object}  MeResponse
// @Failure      401  {object}  ErrorResponse
// @Security     BearerAuth
// @Router       /me [get]
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if id, err := s.sessionStrategy.Authenticate(r); err == nil {
		s.respondMeSession(w, id)
		return
	}
	if entry := s.tokenStrategy.EntryFromRequest(r); entry != nil {
		s.respondMeBearer(w, entry)
		return
	}
	writeError(w, http.StatusUnauthorized, "unauthorized")
}

// respondMeSession builds the session-flavored response: every
// subscription whose stored Username resolves to the caller's
// (account_provider, identifier).
func (s *Server) respondMeSession(w http.ResponseWriter, id *auth.Identity) {
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

	for _, tok := range s.tokenStrategy.List() {
		prov, ident, err := parseTokenUsername(tok.Username)
		if err != nil {
			continue
		}
		if prov != id.AccountProvider || ident != id.Username {
			continue
		}
		resp.Subscriptions = append(resp.Subscriptions, s.tokenListItem(tok))
	}

	if resp.Role == "" {
		resp.Role = accounts.RoleRegular
	}

	writeJSON(w, http.StatusOK, resp)
}

// respondMeBearer builds the bearer-flavored response: profile of
// the account behind the token, plus the single subscription the
// token represents. Clients use this to drive UI without probing
// (e.g. "do I have the mirror ability on this repo?").
func (s *Server) respondMeBearer(w http.ResponseWriter, tok *auth.TokenEntry) {
	resp := MeResponse{
		Username:      tok.Username,
		Subscriptions: []TokenListItem{s.tokenListItem(tok)},
	}
	if prov, ident, err := parseTokenUsername(tok.Username); err == nil {
		resp.Provider = prov
		if acc, accErr := s.accounts.Get(prov, ident); accErr == nil {
			resp.DisplayName = acc.DisplayName
			resp.Email = acc.Email
			resp.Role = acc.Role
		}
	}
	if resp.Role == "" {
		resp.Role = accounts.RoleRegular
	}
	writeJSON(w, http.StatusOK, resp)
}

// tokenListItem materialises the wire-shape for one subscription.
// Pulled out so respondMeSession and respondMeBearer share it.
func (s *Server) tokenListItem(tok *auth.TokenEntry) TokenListItem {
	prov, ident, err := parseTokenUsername(tok.Username)
	hasAccount := false
	accountKey := tok.Username
	if err == nil {
		accountKey = prov + ":" + ident
		hasAccount = s.accounts.Has(prov, ident)
	}
	return TokenListItem{
		Token:         tok.Token,
		Username:      tok.Username,
		Repo:          tok.Repo,
		Abilities:     tok.Abilities,
		HasAccount:    hasAccount,
		Tags:          s.tags.TagsFor(tags.ScopeSubscription, tok.Token),
		EffectiveTags: s.tags.EffectiveSubscriptionTags(tok.Token, tok.Repo, accountKey),
	}
}
