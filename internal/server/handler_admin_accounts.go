package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/petervdpas/GiGot/internal/accounts"
)

// accountView projects a stored Account onto its wire shape. Password
// hash is never shipped — HasPassword is the only leak, and it's
// deliberate: the admin UI needs to know whether a local account is
// "activated" (can log in) or still dormant. The free-function form
// is kept for paths that don't have Server (tests, one-off POST
// responses); it leaves SubscriptionCount=0. Call s.accountView when
// you want the count populated.
func accountView(a accounts.Account) AccountView {
	return AccountView{
		Provider:    a.Provider,
		Identifier:  a.Identifier,
		Role:        a.Role,
		DisplayName: a.DisplayName,
		HasPassword: a.PasswordHash != "",
		CreatedAt:   a.CreatedAt,
	}
}

// accountView (method form) returns the same view with
// SubscriptionCount populated from the current token store. Walks
// every token entry — O(n·m) where n=tokens, m=accounts-in-response.
// At the scale these stores run (tens of tokens, tens of accounts)
// that's fine; if a future deployment pushes either dimension into
// thousands, swap to a single pass that builds a username→count
// map once per request.
func (s *Server) accountView(a accounts.Account) AccountView {
	v := accountView(a)
	v.SubscriptionCount = s.countSubscriptionsFor(a.Provider, a.Identifier)
	return v
}

// countSubscriptionsFor counts token entries whose Username resolves
// to (provider, identifier). Handles both storage shapes: scoped
// "provider:identifier" (post-Phase-3) and bare "identifier" (legacy
// local, accounts.md §6 back-compat).
func (s *Server) countSubscriptionsFor(provider, identifier string) int {
	n := 0
	for _, e := range s.tokenStrategy.List() {
		p, id, err := parseTokenUsername(e.Username)
		if err != nil {
			continue
		}
		if p == provider && id == identifier {
			n++
		}
	}
	return n
}

// splitAccountsPath pulls {provider}/{identifier} out of
// /api/admin/accounts/{provider}/{identifier}. An identifier may itself
// contain further slashes (rare, but OIDC `sub` values sometimes do);
// we only split on the first slash after the provider.
func splitAccountsPath(p string) (provider, identifier string, ok bool) {
	rest := strings.TrimPrefix(p, "/api/admin/accounts/")
	if rest == p || rest == "" {
		return "", "", false
	}
	provider, identifier, found := strings.Cut(rest, "/")
	if !found || provider == "" || identifier == "" {
		return "", "", false
	}
	return provider, identifier, true
}

// handleAdminAccounts godoc
// @Summary      Manage accounts (admin only)
// @Description  GET lists every known account (admins and regulars);
// @Description  POST creates one. Session-cookie authenticated. See
// @Description  docs/design/accounts.md for the data model.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        body  body      CreateAccountRequest   false  "Create body (POST)"
// @Success      200   {object}  AccountListResponse    "GET response"
// @Success      201   {object}  AccountView            "POST response"
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse          "Account already exists"
// @Router       /admin/accounts [get]
// @Router       /admin/accounts [post]
func (s *Server) handleAdminAccounts(w http.ResponseWriter, r *http.Request) {
	if s.requireAdminSession(w, r) == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.adminListAccounts(w, r)
	case http.MethodPost:
		s.adminCreateAccount(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleAdminAccount godoc
// @Summary      Manage one account by (provider, identifier) (admin only)
// @Description  PATCH updates role, display_name, and/or password
// @Description  (local only). DELETE removes the account; the server
// @Description  refuses to remove the last admin so the console can't
// @Description  lock itself out. Session-cookie authenticated.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        provider    path      string                true  "Account provider"
// @Param        identifier  path      string                true  "Account identifier"
// @Param        body        body      UpdateAccountRequest  false "Patch body (PATCH)"
// @Success      200   {object}  AccountView
// @Success      204   "DELETE response"
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse          "Would remove the last admin"
// @Router       /admin/accounts/{provider}/{identifier} [patch]
// @Router       /admin/accounts/{provider}/{identifier} [delete]
func (s *Server) handleAdminAccount(w http.ResponseWriter, r *http.Request) {
	if s.requireAdminSession(w, r) == nil {
		return
	}
	provider, identifier, ok := splitAccountsPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid account path")
		return
	}
	switch r.Method {
	case http.MethodPatch:
		s.adminUpdateAccount(w, r, provider, identifier)
	case http.MethodDelete:
		s.adminDeleteAccount(w, r, provider, identifier)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) adminListAccounts(w http.ResponseWriter, _ *http.Request) {
	items := s.accounts.List()
	views := make([]AccountView, 0, len(items))
	for _, a := range items {
		views = append(views, s.accountView(*a))
	}
	writeJSON(w, http.StatusOK, AccountListResponse{
		Accounts: views,
		Count:    len(views),
	})
}

func (s *Server) adminCreateAccount(w http.ResponseWriter, r *http.Request) {
	var req CreateAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	identifier := strings.ToLower(strings.TrimSpace(req.Identifier))
	if provider == "" || identifier == "" {
		writeError(w, http.StatusBadRequest, "provider and identifier are required")
		return
	}
	if s.accounts.Has(provider, identifier) {
		writeError(w, http.StatusConflict, "account already exists")
		return
	}
	stored, err := s.accounts.Put(accounts.Account{
		Provider:    req.Provider,
		Identifier:  req.Identifier,
		Role:        req.Role,
		DisplayName: req.DisplayName,
	})
	if err != nil {
		if errors.Is(err, accounts.ErrBadProvider) || errors.Is(err, accounts.ErrBadRole) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if provider == accounts.ProviderLocal && req.Password != "" {
		if err := s.accounts.SetPassword(identifier, req.Password); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		refreshed, _ := s.accounts.Get(provider, identifier)
		if refreshed != nil {
			stored = refreshed
		}
	}
	writeJSON(w, http.StatusCreated, s.accountView(*stored))
}

func (s *Server) adminUpdateAccount(w http.ResponseWriter, r *http.Request, provider, identifier string) {
	var req UpdateAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	existing, err := s.accounts.Get(provider, identifier)
	if err != nil {
		if errors.Is(err, accounts.ErrNotFound) {
			writeError(w, http.StatusNotFound, "account not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	newRole := existing.Role
	if req.Role != nil {
		newRole = strings.ToLower(strings.TrimSpace(*req.Role))
	}
	// Demoting the last admin would lock the console out. Count before
	// we write so the check is against the live store, not an in-memory
	// copy that might race with another admin's edit.
	if existing.Role == accounts.RoleAdmin && newRole != accounts.RoleAdmin {
		if countAdmins(s.accounts.List()) <= 1 {
			writeError(w, http.StatusConflict, "cannot demote the last admin")
			return
		}
	}

	newDisplay := existing.DisplayName
	if req.DisplayName != nil {
		newDisplay = *req.DisplayName
	}

	updated, err := s.accounts.Put(accounts.Account{
		Provider:    existing.Provider,
		Identifier:  existing.Identifier,
		Role:        newRole,
		DisplayName: newDisplay,
		CreatedAt:   existing.CreatedAt,
	})
	if err != nil {
		if errors.Is(err, accounts.ErrBadRole) || errors.Is(err, accounts.ErrBadProvider) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if req.Password != nil && *req.Password != "" {
		if existing.Provider != accounts.ProviderLocal {
			writeError(w, http.StatusBadRequest, "password is only valid on local accounts")
			return
		}
		if err := s.accounts.SetPassword(existing.Identifier, *req.Password); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		refreshed, _ := s.accounts.Get(provider, identifier)
		if refreshed != nil {
			updated = refreshed
		}
	}

	writeJSON(w, http.StatusOK, s.accountView(*updated))
}

func (s *Server) adminDeleteAccount(w http.ResponseWriter, _ *http.Request, provider, identifier string) {
	existing, err := s.accounts.Get(provider, identifier)
	if err != nil {
		if errors.Is(err, accounts.ErrNotFound) {
			writeError(w, http.StatusNotFound, "account not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing.Role == accounts.RoleAdmin && countAdmins(s.accounts.List()) <= 1 {
		writeError(w, http.StatusConflict, "cannot remove the last admin")
		return
	}
	if err := s.accounts.Remove(provider, identifier); err != nil {
		if errors.Is(err, accounts.ErrNotFound) {
			writeError(w, http.StatusNotFound, "account not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func countAdmins(list []*accounts.Account) int {
	n := 0
	for _, a := range list {
		if a.Role == accounts.RoleAdmin {
			n++
		}
	}
	return n
}
