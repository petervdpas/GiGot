package server

import (
	"encoding/json"
	"net/http"

	"github.com/petervdpas/GiGot/internal/accounts"
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

// requireAbility is the ability-gate companion to requireAllow. Token
// callers pass iff their TokenEntry carries the named ability; admin
// sessions and auth-disabled (dev) callers bypass. Used on endpoints
// that are off by default for subscribers and must be granted
// explicitly (see remote-sync.md §2.6). Returns 403 on deny.
func (s *Server) requireAbility(w http.ResponseWriter, r *http.Request, ability string) bool {
	ctx := r.Context()
	id := auth.IdentityFromContext(ctx)
	if entry := s.tokenStrategy.EntryFromRequest(r); entry != nil {
		ctx = auth.WithTokenEntry(ctx, entry)
	}
	p := policy.NewTokenAbilityPolicy(ability)
	// Action + resource are ignored by TokenAbilityPolicy; pass placeholders.
	if d := p.Decide(ctx, id, policy.ActionReadRepo, ""); !d.Allowed {
		writeError(w, http.StatusForbidden, "forbidden")
		return false
	}
	return true
}

// resolveRole returns the role of the account behind the request, or
// "" if the identity does not resolve to a stored account. Sessions
// carry AccountProvider directly; bearer tokens carry a scoped
// "provider:identifier" Username we have to parse.
//
// Returning "" rather than an error lets callers treat "unknown
// account" the same as "wrong role" — both deny.
func (s *Server) resolveRole(id *auth.Identity) string {
	if id == nil {
		return ""
	}
	if id.AccountProvider != "" {
		acc, err := s.accounts.Get(id.AccountProvider, id.Username)
		if err != nil {
			return ""
		}
		return acc.Role
	}
	provider, identifier, err := parseTokenUsername(id.Username)
	if err != nil {
		return ""
	}
	acc, err := s.accounts.Get(provider, identifier)
	if err != nil {
		return ""
	}
	return acc.Role
}

// requireMaintainerOrAdmin is the role-gate companion to requireAbility.
// Allows admin and maintainer accounts via either session cookie or
// bearer token; everyone else gets 403. Used on mirror-related
// endpoints so a regular account that somehow holds a key with the
// `mirror` ability still cannot act on it — the role is a structural
// fence on top of the per-token ability bits. See accounts.md §1.
//
// Auth-disabled mode (Provider == ProviderAuthDisabled) bypasses for
// parity with TokenAbilityPolicy/AllowAuthenticated dev semantics.
func (s *Server) requireMaintainerOrAdmin(w http.ResponseWriter, r *http.Request) bool {
	id := auth.IdentityFromContext(r.Context())
	if id == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	if id.Provider == policy.ProviderAuthDisabled {
		return true
	}
	role := s.resolveRole(id)
	if role == accounts.RoleAdmin || role == accounts.RoleMaintainer {
		return true
	}
	writeError(w, http.StatusForbidden, "forbidden")
	return false
}
